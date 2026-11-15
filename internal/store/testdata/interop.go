// Offline acceptance peer; built in a temporary module by sqlite-interop.py.
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/store"
)

type request struct{ Op, Name, Path, Status string }

func main() {
	dir := os.Args[1]
	db, err := store.Open(dir)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	dsn := url.URL{Scheme: "file", Path: filepath.Join(dir, "ianchor.db")}
	raw, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		panic(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if _, err = raw.Exec("PRAGMA busy_timeout=5000"); err != nil {
		panic(err)
	}
	scan := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var r request
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			panic(err)
		}
		var value any
		var err error
		switch r.Op {
		case "list":
			value, err = db.AllEntries()
		case "get":
			value, err = db.GetEntry(r.Name)
		case "add":
			err = db.AddEntry(r.Name, &r.Path, "file", "linked")
		case "status":
			err = db.SetStatus(r.Name, r.Status)
		case "path":
			err = db.SetOriginalPath(r.Name, &r.Path)
		case "update":
			err = db.SetStatus(r.Name, r.Status)
			if err == nil {
				err = db.SetOriginalPath(r.Name, &r.Path)
			}
		case "delete":
			err = db.DeleteEntry(r.Name)
		case "lock":
			_, err = raw.Exec("BEGIN IMMEDIATE")
		case "unlock":
			_, err = raw.Exec("ROLLBACK")
		case "meta":
			var schema, journal string
			err = raw.QueryRow("SELECT sql FROM sqlite_master WHERE name='entries'").Scan(&schema)
			if err == nil {
				err = raw.QueryRow("PRAGMA journal_mode").Scan(&journal)
			}
			value = map[string]any{"schema": schema, "journal": journal}
		case "readonly":
			var ro *store.DB
			ro, err = store.OpenReadOnly(dir)
			if err == nil {
				value, err = ro.AllEntries()
				if err == nil && ro.AddEntry("readonly-must-not-exist", nil, "file", "linked") != store.ErrReadOnly {
					err = fmt.Errorf("readonly write was not rejected")
				}
				ro.Close()
			}
		case "missing":
			var ro *store.DB
			ro, err = store.OpenReadOnly(r.Path)
			if err == nil {
				ro.Close()
				err = fmt.Errorf("missing readonly database opened")
			} else {
				err = nil
			}
		default:
			err = fmt.Errorf("unknown op %q", r.Op)
		}
		response := map[string]any{"value": value}
		if err != nil {
			response["error"] = store.NormalizeError(err).Error()
		}
		if err = out.Encode(response); err != nil {
			panic(err)
		}
	}
	if err = scan.Err(); err != nil {
		panic(err)
	}
}
