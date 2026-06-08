// Command mykeep is a portable, USB-resident, encrypted memory store for AI agents.
//
// Launching with no arguments (e.g. double-clicking the drive launcher) opens the
// cross-platform GUI: a local web app in your browser that collects the password,
// unlocks the encrypted DB, and shows a dashboard. `mykeep serve` is the terminal
// equivalent (prompts for the password on the TTY).
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mykeep.ai/internal/app"
	"mykeep.ai/internal/config"
	"mykeep.ai/internal/gui"
	"mykeep.ai/internal/paths"
	"mykeep.ai/internal/secret"
	"mykeep.ai/internal/server"
	"mykeep.ai/internal/setup"
)

var version = "0.1.0-dev"

func main() {
	cmd := "gui" // default: double-click launches the GUI
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	var err error
	var args []string
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	switch cmd {
	case "gui":
		err = cmdGUI()
	case "serve":
		err = cmdServe()
	case "snippet":
		err = cmdSnippet()
	case "guide":
		err = cmdGuide()
	case "doctor":
		err = cmdDoctor()
	case "capture":
		err = cmdCapture(args)
	case "retain":
		err = cmdRetain(args)
	case "recall":
		err = cmdRecall(args)
	case "memories":
		err = cmdMemories(args)
	case "banks":
		err = cmdBanks(args)
	case "version":
		sqliteVer, vec, _ := runtimeInfo()
		fmt.Printf("mykeep %s\n  sqlite %s, vec0 %s\n", version, sqliteVer, yesno(vec, "available", "unavailable"))
	default:
		fmt.Fprintln(os.Stderr, "usage: mykeep <gui|serve|snippet|guide|doctor|capture|retain|recall|memories|banks|version>")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mykeep: "+err.Error())
		os.Exit(1)
	}
}

// cmdGUI launches the browser GUI (the default).
func cmdGUI() error {
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	if !layout.Portable {
		fmt.Fprintln(os.Stderr, "warning: running non-portable — config/DB are on the host, not the stick")
	}
	addr := "127.0.0.1:8765"
	if !layout.IsFirstLaunch() {
		if c, err := config.Load(layout.ConfigPath()); err == nil && c.Server.Addr != "" {
			addr = c.Server.Addr
		}
	}
	return gui.New(layout, version, addr).Run()
}

func cmdSnippet() error {
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigPath())
	if err != nil {
		return errors.New("not set up yet; run mykeep first")
	}
	fmt.Println(server.SnippetText(cfg.Server.Addr, ""))
	return nil
}

// cmdGuide prints the full agent operating manual (no server needed).
func cmdGuide() error {
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	addr := "127.0.0.1:8765"
	if c, err := config.Load(layout.ConfigPath()); err == nil && c.Server.Addr != "" {
		addr = c.Server.Addr
	}
	fmt.Println(server.GuideText(addr))
	return nil
}

// cmdServe is the terminal launch: prompt for the password, then serve the REST API.
func cmdServe() error {
	ctx := context.Background()
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	if !layout.Portable {
		fmt.Fprintln(os.Stderr, "warning: running non-portable — config/DB are on the host, not the stick")
	}

	rt, err := unlockTTY(ctx, layout)
	if err != nil {
		return err
	}

	token := ""
	if rt.Config.Server.RequireToken {
		token = randToken()
	}
	srv := server.New(rt.Config, rt.Store, rt.Ingest, rt.Recall, version, rt.EmbedderName(), layout.Portable, token)
	httpSrv := &http.Server{Addr: rt.Config.Server.Addr, Handler: srv.Handler()}
	errCh := make(chan error, 1)
	go func() {
		if e := httpSrv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			errCh <- e
		}
	}()

	fmt.Printf("\n✅ mykeep running at http://%s  — copy the block below into your AI assistant:\n", rt.Config.Server.Addr)
	fmt.Println(srv.Snippet())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		fmt.Fprintln(os.Stderr, "\nshutting down: flushing memories…")
	case e := <-errCh:
		return e
	}
	shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	return rt.Close()
}

// unlockTTY prompts on the terminal (first launch creates the password) and builds
// the runtime.
func unlockTTY(ctx context.Context, layout paths.Layout) (*app.Runtime, error) {
	if layout.IsFirstLaunch() {
		fmt.Fprintln(os.Stderr, "First launch — let's set up your encrypted memory.")
		pw, err := setup.ReadNewPassphrase()
		if err != nil {
			return nil, err
		}
		defer wipe(pw)
		return app.Open(ctx, layout, pw, true, version)
	}
	for tries := 0; tries < 3; tries++ {
		pw, err := setup.ReadPassphrase("Enter decryption password: ")
		if err != nil {
			return nil, err
		}
		rt, err := app.Open(ctx, layout, pw, false, version)
		wipe(pw)
		if err == nil {
			return rt, nil
		}
		if !errors.Is(err, secret.ErrWrongPassphrase) {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, "  wrong password.")
	}
	return nil, errors.New("too many failed attempts")
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
