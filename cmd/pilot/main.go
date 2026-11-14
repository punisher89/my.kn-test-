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
	engine := flag.String("engine-dir", "", "путь движка при --init")
	printArgs := flag.Bool("print-engine-args", false, "вывести JSON-массив параметров nfqws2")
	flag.Parse()
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
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	m.Boot()
	watchCtx, stopWatch := context.WithCancel(ctx)
	go m.Watch(watchCtx)
	server := &http.Server{Handler: pilot.Handler(m, strings.TrimSpace(string(token))), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
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
	return errorsJoin(err, m.Close())
}
func errorsJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
