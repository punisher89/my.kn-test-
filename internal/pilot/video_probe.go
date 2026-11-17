package pilot

import (
    "context"
    "crypto/tls"
    "errors"
    "fmt"
    "io"
    "mime"
    "net"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "time"
)

const videoProbeLimit = 64 << 10

// VideoProbeResult deliberately contains no URL, signed query, client IP or headers
// that might expose an expiring YouTube playback token.
type VideoProbeResult struct {
    OK bool `json:"ok"`
    Status int `json:"status"`
    MediaType string `json:"media_type,omitempty"`
    ContentRange string `json:"content_range,omitempty"`
    Bytes int `json:"bytes"`
    HeadersMS int64 `json:"headers_ms"`
    FirstByteMS int64 `json:"first_byte_ms,omitempty"`
    DurationMS int64 `json:"duration_ms"`
    Error string `json:"error,omitempty"`
}

// A generic HTTPS response from a YouTube CDN host does not prove video playback.
func isYouTubeCDNHost(host string) bool {
    return strings.HasSuffix(strings.ToLower(host), ".googlevideo.com")
}

func parseVideoURL(raw string, now time.Time) (*url.URL, error) {
    if len(raw) < 50 || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n") {
        return nil, errors.New("нужна действующая временная ссылка на видеопоток")
    }
    u, err := url.Parse(raw)
    if err != nil || u == nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" {
        return nil, errors.New("разрешена только HTTPS-ссылка на YouTube CDN")
    }
    host := strings.ToLower(u.Hostname())
    if !isYouTubeCDNHost(host) || u.Port() != "" && u.Port() != "443" || !strings.HasPrefix(u.EscapedPath(), "/videoplayback") || u.RawQuery == "" {
        return nil, errors.New("нужен адрес videoplayback на поддомене googlevideo.com")
    }
    expires := u.Query().Get("expire")
    if expires == "" {
        return nil, errors.New("в ссылке нет временного параметра expire")
    }
    unix, err := strconv.ParseInt(expires, 10, 64)
    if err != nil || unix <= now.Add(10*time.Second).Unix() || unix > now.Add(24*time.Hour).Unix() {
        return nil, errors.New("ссылка истекла или время её действия некорректно")
    }
    return u, nil
}

func videoContentRange(raw string) (int, error) {
    s, ok := strings.CutPrefix(raw, "bytes ")
    if !ok { return 0, errors.New("нет корректного Content-Range") }
    bounds, total, ok := strings.Cut(s, "/")
    if !ok { return 0, errors.New("нет корректного Content-Range") }
    first, last, ok := strings.Cut(bounds, "-")
    if !ok { return 0, errors.New("нет корректного Content-Range") }
    a, e1 := strconv.ParseInt(first, 10, 64)
    b, e2 := strconv.ParseInt(last, 10, 64)
    size, e3 := strconv.ParseInt(total, 10, 64)
    if e1 != nil || e2 != nil || e3 != nil || a != 0 || b < 0 || b >= videoProbeLimit || size <= b ||
        b != int64(videoProbeLimit-1) && b != size-1 {
        return 0, errors.New("сервер вернул неожиданный диапазон")
    }
    return int(b + 1), nil
}

func readVideoFragment(ctx context.Context, resp *http.Response, start time.Time, result VideoProbeResult) VideoProbeResult {
    result.Status = resp.StatusCode
    result.DurationMS = time.Since(start).Milliseconds()
    if resp.StatusCode != http.StatusPartialContent {
        result.Error = fmt.Sprintf("HTTP %d вместо 206 Partial Content", resp.StatusCode)
        return result
    }
    contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
    if err != nil || !strings.HasPrefix(contentType, "video/") {
        result.Error = "получен не видеофрагмент (тип содержимого не video/*)"
        return result
    }
    result.MediaType = contentType
    needed, err := videoContentRange(resp.Header.Get("Content-Range"))
    if err != nil {
        result.Error = err.Error()
        return result
    }
    result.ContentRange = fmt.Sprintf("bytes 0-%d", needed-1)
    if resp.ContentLength >= 0 && resp.ContentLength != int64(needed) {
        result.Error = "Content-Length не совпадает с диапазоном"
        return result
    }
    buffer := make([]byte, 8192)
    emptyReads := 0
    for result.Bytes <= videoProbeLimit {
        if ctx.Err() != nil {
            result.Error = "таймаут получения видеофрагмента"
            break
        }
        count := len(buffer)
        if left := videoProbeLimit + 1 - result.Bytes; left < count { count = left }
        n, readErr := resp.Body.Read(buffer[:count])
        if n > 0 {
            if result.Bytes == 0 { result.FirstByteMS = time.Since(start).Milliseconds() }
            result.Bytes += n
            emptyReads = 0
        } else { emptyReads++ }
        if readErr == io.EOF { break }
        if readErr != nil {
            result.Error = "не удалось полностью получить видеофрагмент"
            break
        }
        if emptyReads >= 4 {
            result.Error = "сервер не передаёт видеоданные"
            break
        }
    }
    result.DurationMS = time.Since(start).Milliseconds()
    if result.Error == "" && result.Bytes != needed {
        result.Error = "размер полученного видеофрагмента не совпадает с Content-Range"
    }
    result.OK = result.Error == ""
    return result
}

func probeVideoFragment(ctx context.Context, u *url.URL) VideoProbeResult {
    var result VideoProbeResult
    start := time.Now()
    ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
    defer cancel()
    transport := &http.Transport{
        Proxy: nil, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false,
        TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()},
        DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
            host, port, err := net.SplitHostPort(address)
            if err != nil || !strings.EqualFold(host, u.Hostname()) || port != "443" {
                return nil, errors.New("неожиданный узел или порт")
            }
            return (HTTPProber{}).dialPublic(ctx, host, port, "tcp4", 12*time.Second, false)
        },
    }
    defer transport.CloseIdleConnections()
    request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
    if err != nil {
        result.Error = "не удалось подготовить запрос"
        return result
    }
    request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", videoProbeLimit-1))
    request.Header.Set("Accept-Encoding", "identity")
    request.Header.Set("User-Agent", "Keenetic-Pilot/"+Version)
    client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
    resp, err := client.Do(request)
    if err != nil {
        // url.Error may include the entire signed videoplayback URL. Never return it.
        result.Error = "не удалось подключиться к CDN или завершить TLS"
        result.DurationMS = time.Since(start).Milliseconds()
        return result
    }
    defer resp.Body.Close()
    result.HeadersMS = time.Since(start).Milliseconds()
    return readVideoFragment(ctx, resp, start, result)
}

// CheckYouTubeVideo does not persist the signed URL, change strategies, restart
// nfqws2 or write reports. A webpage check remains distinct from a video check.
func (m *Manager) CheckYouTubeVideo(ctx context.Context, raw string) (VideoProbeResult, error) {
    u, err := parseVideoURL(raw, time.Now())
    if err != nil { return VideoProbeResult{}, err }
    if !m.op.TryLock() { return VideoProbeResult{}, ErrBusy }
    defer m.op.Unlock()
    m.mu.Lock()
    now := time.Now()
    if now.Sub(m.lastVideoProbeAt) < 15*time.Second {
        m.mu.Unlock()
        return VideoProbeResult{}, errors.New("повторная проверка доступна через 15 секунд")
    }
    m.lastVideoProbeAt = now
    m.mu.Unlock()
    return probeVideoFragment(ctx, u), nil
}
