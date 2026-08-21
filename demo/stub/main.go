// Command stub serves just enough of the Taiga and OpenProject APIs to record
// the README demo against the real zygos binary, with no real credentials and
// no network. It is a demo fixture, not part of the shipped tool.
//
//	go run ./demo/stub -addr :8099
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", ":8099", "listen address")
	flag.Parse()

	mux := http.NewServeMux()

	// ─── Taiga ────────────────────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/auth", json(`{"auth_token":"demo-token","id":1}`))
	mux.HandleFunc("/api/v1/projects", json(`[
		{"id":1,"name":"Portal Cliente","slug":"portal-cliente"},
		{"id":2,"name":"Migración ERP","slug":"migracion-erp"}
	]`))
	mux.HandleFunc("/api/v1/userstories", json(`[
		`+story(31, 1, "Portal Cliente", "Rediseñar el flujo de alta de usuarios", "In progress", "2026-08-28")+`,
		`+story(34, 2, "Portal Cliente", "Exportar reportes a CSV", "New", "")+`,
		`+story(41, 3, "Migración ERP", "Mapear tablas de facturación", "In progress", "2026-09-04")+`
	]`))
	mux.HandleFunc("/api/v1/tasks", json(`[
		`+story(88, 12, "Portal Cliente", "Validar tokens de sesión expirados", "New", "")+`
	]`))

	// ─── OpenProject ──────────────────────────────────────────────────────────
	mux.HandleFunc("/api/v3/users/me", json(`{"id":9,"name":"Gabriel Funes"}`))
	mux.HandleFunc("/api/v3/projects", json(`{"total":1,"count":1,"_embedded":{"elements":[
		{"id":7,"name":"Legacy Billing","identifier":"legacy-billing"}
	]}}`))
	mux.HandleFunc("/api/v3/work_packages", json(`{"total":2,"count":2,"_embedded":{"elements":[
		`+wp(2041, "Reemplazar el cron de conciliación", "In progress", "High", "Legacy Billing", "2026-08-25")+`,
		`+wp(2043, "Documentar el endpoint de reintentos", "New", "Normal", "Legacy Billing", "")+`
	]}}`))

	log.Printf("demo stub listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func json(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.TrimSpace(body))
	}
}

func story(id, ref int, project, subject, status, due string) string {
	dueField := "null"
	if due != "" {
		dueField = `"` + due + `"`
	}
	return fmt.Sprintf(`{"id":%d,"ref":%d,"subject":%q,
		"project_extra_info":{"name":%q,"slug":"p","id":1},
		"status_extra_info":{"name":%q,"is_closed":false},
		"assigned_to_extra_info":{"full_name_display":"Gabriel Funes"},
		"due_date":%s,"created_date":"2026-08-01T10:00:00Z","modified_date":"2026-08-18T09:00:00Z"}`,
		id, ref, subject, project, status, dueField)
}

func wp(id int, subject, status, priority, project, due string) string {
	dueField := "null"
	if due != "" {
		dueField = `"` + due + `"`
	}
	return fmt.Sprintf(`{"id":%d,"subject":%q,"dueDate":%s,
		"description":{"raw":""},"createdAt":"2026-08-02T10:00:00Z","updatedAt":"2026-08-19T09:00:00Z",
		"_links":{"status":{"title":%q,"href":"/api/v3/statuses/1"},
		"priority":{"title":%q},"project":{"title":%q},
		"assignee":{"title":"Gabriel Funes"}}}`,
		id, subject, dueField, status, priority, project)
}
