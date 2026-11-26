package pilot

import (
 "context"
 "errors"
 "net"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "strconv"
 "strings"
 "time"
)

type sshPasswordRequest struct {
 Host string `json:"host"`
 User string `json:"user"`
 Port string `json:"port"`
 Password string `json:"password"`
}

var sshHostPattern=regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$")
var sshUserPattern=regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_.-]{0,63}$")

func sshSecretTransport(r *http.Request) bool {
 if r.TLS!=nil{return true}
 remote,_,err:=net.SplitHostPort(r.RemoteAddr)
 if err!=nil||!net.ParseIP(remote).IsLoopback(){return false}
 host:=r.Host
 if h,_,e:=net.SplitHostPort(host);e==nil{host=h}
 return host=="localhost"||net.ParseIP(strings.Trim(host,"[]")).IsLoopback()
}

func validateSSHPassword(in sshPasswordRequest) error {
 host:=strings.Trim(in.Host,"[]")
 port,err:=strconv.Atoi(in.Port)
 if (!sshHostPattern.MatchString(host)&&net.ParseIP(host)==nil)||!sshUserPattern.MatchString(in.User)||err!=nil||port<1||port>65535||len(in.Port)>5 {
  return errors.New("Проверь адрес, логин и порт SSH")
 }
 if len(in.Password)==0||len(in.Password)>512||strings.ContainsAny(in.Password,"\r\n\x00"){return errors.New("Недопустимый формат пароля")}
 return nil
}

// A single authenticated no-op; no tunnel, routes or persistent credentials.
func checkSSHPassword(ctx context.Context, configPath string, in sshPasswordRequest) map[string]string {
 fail:=func(msg string)map[string]string{return map[string]string{"status":"unavailable","message":msg}}
 ssh,err:=exec.LookPath("ssh")
 if err!=nil{return fail("OpenSSH client не найден")}
 pass,err:=exec.LookPath("sshpass")
 if err!=nil{return fail("sshpass не найден")}
 known:=filepath.Join(filepath.Dir(configPath),"vps-known-hosts")
 st,err:=os.Lstat(known)
 if err!=nil||!st.Mode().IsRegular()||st.Size()==0||st.Mode().Perm()&0022!=0{return fail("Нужен проверенный файл vps-known-hosts")}
 ctx,cancel:=context.WithTimeout(ctx,15*time.Second)
 defer cancel()
 cmd:=exec.CommandContext(ctx,pass,"-d","0",ssh,"-F","/dev/null","-n","-T",
  "-o","UserKnownHostsFile="+known,"-o","GlobalKnownHostsFile=/dev/null",
  "-o","StrictHostKeyChecking=yes","-o","NumberOfPasswordPrompts=1","-o","ConnectTimeout=8",
  "-o","PreferredAuthentications=password","-o","PubkeyAuthentication=no","-o","BatchMode=no",
  "-o","ForwardAgent=no","-o","ForwardX11=no","-o","ClearAllForwardings=yes",
  "-p",in.Port,"-l",in.User,"--",strings.Trim(in.Host,"[]"),"true")
 cmd.Stdin=strings.NewReader(in.Password+"\n")
 // Do not expose stderr or command output, which can contain remote text.
 if err:=cmd.Run();err!=nil{
  if ctx.Err()!=nil{return fail("Таймаут проверки")}
  var exit *exec.ExitError
  if errors.As(err,&exit)&&exit.ExitCode()==5{return fail("SSH отклонил пароль")}
  return fail("SSH-вход не подтверждён: проверь ключ сервера, доступность и разрешение входа по паролю")
 }
 return map[string]string{"status":"authenticated"}
}
