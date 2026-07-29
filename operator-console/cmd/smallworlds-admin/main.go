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

	"github.com/stephan271/smallworlds/operator-console/internal/buildinfo"
	"github.com/stephan271/smallworlds/operator-console/internal/consoleserve"
	"github.com/stephan271/smallworlds/operator-console/internal/launcher"
	"github.com/stephan271/smallworlds/operator-console/internal/singleinstance"
	"github.com/stephan271/smallworlds/operator-console/internal/webui"
)

func main() {
	// The same binary is both products: the Bootstrap Launcher on an Operator's
	// computer, and the in-cluster Operator Console in a pod. A container has no
	// user configuration directory, so the launcher's default data directory is
	// resolved leniently here and only insisted on when the launcher actually runs.
	defaultDataDir := ""
	if configDir, err := os.UserConfigDir(); err == nil {
		defaultDataDir = filepath.Join(configDir, "smallworlds", "operator-console")
	}
	port := flag.Int("port", 0, "loopback port; zero selects a random port")
	dataDir := flag.String("data-dir", defaultDataDir, "launcher data directory")
	launchToken := flag.String("token", "", "fixed launch token for controlled testing")
	noBrowser := flag.Bool("no-browser", false, "do not open the browser")
	showVersion := flag.Bool("version", false, "print the launcher version and exit")
	serveConsole := flag.Bool("serve-console", false, "run the in-cluster Operator Console instead of the Bootstrap Launcher")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Version)
		return
	}
	if *serveConsole {
		if err := serveInCluster(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *dataDir == "" {
		log.Fatal("no launcher data directory: pass --data-dir")
	}

	var err error
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

// serveInCluster runs the Operator Console inside the cluster: Keycloak OIDC
// instead of a loopback token, live cluster observers instead of a Setup
// Journey, and a listener on the pod's port instead of 127.0.0.1.
func serveInCluster() error {
	settings, err := consoleserve.SettingsFromEnvironment()
	if err != nil {
		return err
	}
	// Startup reaches Keycloak and the API server. Bounding it means a pod whose
	// dependencies are not up yet fails its probe and is restarted, rather than
	// hanging forever in a state nothing reports.
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelStartup()
	server, err := consoleserve.New(startupContext, settings)
	if err != nil {
		return err
	}
	if len(settings.SessionKey) == 0 {
		log.Print("no SMALLWORLDS_SESSION_KEY configured: sessions are signed with a per-process key, so a restart signs every Operator out")
	}

	listener, err := net.Listen("tcp", settings.Address)
	if err != nil {
		return err
	}
	log.Printf("SmallWorlds Operator Console listening on %s for %s", settings.Address, settings.ExternalURL)
	httpServer := &http.Server{
		Handler:           server.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		<-shutdownSignal.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownContext)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func openBrowser(url string) error {
	command, err := browserCommand(runtime.GOOS, url)
	if err != nil {
		return err
	}
	return command.Start()
}

// browserCommand keeps platform browser invocation explicit and testable. The
// launcher never shells out through a command string, so the one-time loopback
// URL cannot be interpreted as a shell argument.
func browserCommand(platform, url string) (*exec.Cmd, error) {
	var command *exec.Cmd
	switch platform {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	default:
		return nil, fmt.Errorf("opening a browser is unsupported on %s", platform)
	}
	return command, nil
}
