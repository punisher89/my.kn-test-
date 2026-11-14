package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/punisher89/my.kn-test-/internal/pilot"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "/opt/etc/keenetic-pilot/config.json", "конфигурация")
	mode := flag.String("mode", "router", "router или development")
	initOnly := flag.Bool("init", false, "создать конфигурацию и ключ доступа; не запускать")
	listen := flag.String("listen", "", "адрес панели при --init")
	wan := flag.String("wan", "", "WAN-интерфейс при --init")
	engine := flag.String("engine-dir", "", "путь движка при --init/--check-engine/--doctor")
	printArgs := flag.Bool("print-engine-args", false, "вывести JSON-массив параметров nfqws2")
	versionOnly := flag.Bool("version", false, "версия контроллера")
	checkOnly := flag.Bool("check-config", false, "проверить конфигурацию без запуска")
	checkEngine := flag.Bool("check-engine", false, "проверить параметры и Lua настоящим nfqws2 без перехвата")
	doctor := flag.Bool("doctor", false, "диагностика предпосылок без изменения firewall")
	health := flag.Bool("health", false, "состояние уже запущенного контроллера через локальный сокет")
	repair := flag.Bool("repair-firewall", false, "восстановить правила через запущенный контроллер")
	pidFile := flag.String("pidfile", "", "файл PID службы; записывает только экземпляр, получивший блокировку")
	flag.Parse()
	if *versionOnly {
		fmt.Println(pilot.Version)
		return nil
	}
	if *health || *repair {
		method, path := http.MethodGet, "/healthz"
		if *repair {
			method, path = http.MethodPost, "/repair"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		data, err := pilot.ControlRequest(ctx, filepath.Dir(*configPath), method, path)
		if err != nil {
			return err
		}
		if len(data) > 0 {
			_, err = os.Stdout.Write(data)
		}
		return err
	}
	if *mode != "router" && *mode != "development" {
		return fmt.Errorf("неизвестный режим %q", *mode)
	}
	tokenPath := filepath.Join(filepath.Dir(*configPath), "access-token")
	if *initOnly {
		if _, err := os.Stat(*configPath); !os.IsNotExist(err) {
			return fmt.Errorf("конфигурация уже существует либо недоступна: %s", *configPath)
		}
		if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
			return fmt.Errorf("ключ уже существует либо недоступен: %s", tokenPath)
		}
		c := pilot.DefaultConfig()
		c.WAN = *wan
		if *listen != "" {
			c.Listen = *listen
		}
		if *engine != "" {
			c.EngineDir = *engine
		}
		if err := c.Validate(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*configPath), 0700); err != nil {
			return err
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		f, err := os.OpenFile(tokenPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(hex.EncodeToString(random) + "\n")
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := pilot.WriteJSON(*configPath, c); err != nil {
			_ = os.Remove(tokenPath)
			return err
		}
		fmt.Println("Создано:", *configPath, "\nКлюч доступа:", tokenPath)
		return nil
	}
	c, err := pilot.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if *printArgs {
		return json.NewEncoder(os.Stdout).Encode(pilot.EngineArgs(c))
	}
	if *checkOnly {
		fmt.Println("Конфигурация корректна")
		return nil
	}
	if *engine != "" && (*checkEngine || *doctor) {
		c.EngineDir = *engine
	}
	if *checkEngine {
		if err := pilot.CheckEngine(c); err != nil {
			return err
		}
		fmt.Println("nfqws2 и Lua проверены без запуска перехвата")
		return nil
	}
	if *doctor {
		checks := pilot.Diagnose(c)
		if err := json.NewEncoder(os.Stdout).Encode(checks); err != nil {
			return err
		}
		for _, check := range checks {
			if !check.OK {
				return fmt.Errorf("предпосылки не выполнены; смотрите отчёт")
			}
		}
		return nil
	}
	lock, err := pilot.LockInstance(filepath.Dir(*configPath))
	if err != nil {
		return err
	}
	defer lock.Close()
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(token))) < 32 {
		return fmt.Errorf("слишком короткий ключ доступа")
	}
	buffer := &pilot.LogBuffer{}
	var backend pilot.Backend = pilot.Development{}
	if *mode == "router" {
		backend = pilot.NewRouter(filepath.Dir(*configPath), buffer)
	}
	m := pilot.NewManager(c, *configPath, backend, pilot.HTTPProber{MarkDirect: *mode == "router"}, buffer)
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	controlListener, err := pilot.ControlListener(filepath.Dir(*configPath))
	if err != nil {
		return err
	}
	defer controlListener.Close()
	if *pidFile != "" {
		if !filepath.IsAbs(*pidFile) {
			return fmt.Errorf("pidfile должен быть абсолютным")
		}
		if err := pilot.WriteJSON(*pidFile, os.Getpid()); err != nil {
			return err
		}
		defer func() {
			if b, err := os.ReadFile(*pidFile); err == nil && strings.TrimSpace(string(b)) == fmt.Sprint(os.Getpid()) {
				_ = os.Remove(*pidFile)
			}
		}()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	m.Boot()
	watchCtx, stopWatch := context.WithCancel(ctx)
	go m.Watch(watchCtx)
	server := &http.Server{Handler: pilot.Handler(m, strings.TrimSpace(string(token))), BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	controlServer := &http.Server{Handler: pilot.ControlHandler(m), ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		if err := controlServer.Serve(controlListener); err != nil && err != http.ErrServerClosed {
			buffer.Add(err.Error())
			cancel()
		}
	}()
	log.Printf("Keenetic Pilot %s; mode=%s; listen=%s", pilot.Version, *mode, c.Listen)
	select {
	case <-ctx.Done():
	case err = <-done:
		if err == http.ErrServerClosed {
			err = nil
		}
	}
	stopWatch()
	m.CancelTune()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	_ = controlServer.Shutdown(shutdownCtx)
	return errorsJoin(err, m.Close())
}
func errorsJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
