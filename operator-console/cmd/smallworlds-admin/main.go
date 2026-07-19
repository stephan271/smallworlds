package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/singleinstance"
	"github.com/stephan271/smallworlds/operator-console/internal/webui"
)

func main() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	defaultDataDir := filepath.Join(configDir, "smallworlds", "operator-console")
	port := flag.Int("port", 0, "loopback port; zero selects a random port")
	dataDir := flag.String("data-dir", defaultDataDir, "launcher data directory")
	launchToken := flag.String("token", "", "fixed launch token for controlled testing")
	noBrowser := flag.Bool("no-browser", false, "do not open the browser")
	flag.Parse()

	token := *launchToken
	if token == "" {
		token, err = randomToken()
		if err != nil {
			log.Fatal(err)
		}
	}
	lease, err := singleinstance.Acquire(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if !lease.IsOwner() {
		if *noBrowser {
			log.Printf("SmallWorlds Bootstrap Launcher is already running at %s", lease.ExistingURL())
			return
		}
		if err := openBrowser(lease.ExistingURL()); err != nil {
			log.Fatal(err)
		}
		return
	}
	defer lease.Close()
	api, err := launcher.New(launcher.Config{DataDir: *dataDir, LaunchToken: token})
	if err != nil {
		log.Fatal(err)
	}
	defer api.Close()

	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	if err := lease.Publish(address + "/"); err != nil {
		log.Fatal(err)
	}
	launchURL := address + "/?token=" + token
	if !*noBrowser {
		if err := openBrowser(launchURL); err != nil {
			log.Printf("Open %s in your browser (%v)", launchURL, err)
		}
	}
	log.Printf("SmallWorlds Bootstrap Launcher listening on %s", address)
	httpServer := &http.Server{
		Handler:           webui.New(api),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-shutdownSignal.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownContext)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Start()
}
