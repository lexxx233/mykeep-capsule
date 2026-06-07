package main

import (
	"database/sql"
	"fmt"
	"os"

	"joyvend.io/internal/config"
	"joyvend.io/internal/paths"
	"joyvend.io/internal/store"
)

// runtimeInfo probes the embedded SQLite (version + whether the vec0 KNN backend is
// registered) without needing the password — it uses a throwaway in-memory DB.
func runtimeInfo() (sqliteVer string, vecAvail, fts5 bool) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return "?", false, false
	}
	defer db.Close()
	_ = db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVer)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE _v USING vec0(e float[2])`); err == nil {
		vecAvail = true
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE _f USING fts5(x)`); err == nil {
		fts5 = true
	}
	return
}

// cmdDoctor prints diagnostics that don't require the password.
func cmdDoctor() error {
	layout, err := paths.Resolve()
	if err != nil {
		return err
	}
	sqliteVer, vec, fts5 := runtimeInfo()

	fmt.Println("joyvend doctor")
	fmt.Printf("  version:        %s\n", version)
	fmt.Printf("  sqlite:         %s\n", sqliteVer)
	fmt.Printf("  vec0 backend:   %s\n", yesno(vec, "available", "unavailable (brute-force)"))
	fmt.Printf("  fts5:           %s\n", yesno(fts5, "ok", "MISSING"))
	fmt.Printf("  data dir:       %s\n", layout.DataDir)
	fmt.Printf("  portable:       %s\n", yesno(layout.Portable, "yes (on the stick)", "no (host fallback)"))

	if layout.IsFirstLaunch() {
		fmt.Println("  setup:          not set up yet (run joyvend to create a password)")
	} else {
		fmt.Println("  setup:          configured")
		if c, err := config.Load(layout.ConfigPath()); err == nil {
			fmt.Printf("  embedder:       %s (dim %d)\n", c.Embedding.Model, c.Embedding.Dim)
			fmt.Printf("  server addr:    %s\n", c.Server.Addr)
			fmt.Printf("  soft cap:       %d MB\n", c.Runtime.SoftCapMB)
		}
	}
	if fi, err := os.Stat(layout.DBPath()); err == nil {
		fmt.Printf("  encrypted db:   %s (%d bytes)\n", layout.DBPath(), fi.Size())
	} else {
		fmt.Println("  encrypted db:   (none yet)")
	}
	fmt.Printf("  instance:       %s\n", yesno(store.IsRunning(layout.DBPath()), "another instance is running", "free"))
	return nil
}

func yesno(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}
