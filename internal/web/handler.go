package web

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/larsellefsen/spectarr/internal/config"
	"github.com/larsellefsen/spectarr/internal/radarr"
	"github.com/larsellefsen/spectarr/internal/scheduler"
)

//go:embed templates/*
var templateFS embed.FS

type Handler struct {
	store     *config.Store
	scheduler *scheduler.Scheduler
}

func NewHandler(store *config.Store, sched *scheduler.Scheduler) (*Handler, error) {
	return &Handler{store: store, scheduler: sched}, nil
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.dashboard)
	r.Get("/settings", h.settingsPage)
	r.Post("/settings", h.saveSettings)
	r.Post("/run", h.triggerRun)
	return r
}

// ---- Dashboard ----

type dashboardData struct {
	Logs []config.RunLog
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	logs, err := h.store.RecentRunLogs(20)
	if err != nil {
		log.Printf("web: fetch run logs: %v", err)
	}
	h.render(w, "index.html", dashboardData{Logs: logs})
}

// ---- Settings ----

type settingsData struct {
	Config           map[string]string
	QualityProfiles  []radarr.QualityProfile
	RootFolders      []radarr.RootFolder
	SavedMessage     string
	Error            string
}

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "settings.html", h.buildSettingsData("", ""))
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, "settings.html", h.buildSettingsData("", "Invalid form data"))
		return
	}

	fields := []string{
		"specto_email", "specto_password",
		"rating_threshold",
		"radarr_url", "radarr_api_key",
		"radarr_quality_profile_id", "radarr_root_folder_path",
		"poll_interval_minutes",
	}
	for _, f := range fields {
		val := r.FormValue(f)
		if err := h.store.Set(f, val); err != nil {
			h.render(w, "settings.html", h.buildSettingsData("", "Save failed: "+err.Error()))
			return
		}
	}

	// If HTMX request, return just the settings partial with a success banner.
	if r.Header.Get("HX-Request") == "true" {
		data := h.buildSettingsData("Settings saved.", "")
		h.render(w, "settings.html", data)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) buildSettingsData(saved, errMsg string) settingsData {
	d := settingsData{
		Config:       h.store.GetAll(),
		SavedMessage: saved,
		Error:        errMsg,
	}
	radarrURL := h.store.Get("radarr_url")
	radarrKey := h.store.Get("radarr_api_key")
	if radarrURL != "" && radarrKey != "" {
		rc := radarr.New(radarrURL, radarrKey)
		d.QualityProfiles, _ = rc.GetQualityProfiles()
		d.RootFolders, _ = rc.GetRootFolders()
	}
	return d
}

// ---- Manual run ----

type runPartialData struct {
	Logs []config.RunLog
}

func (h *Handler) triggerRun(w http.ResponseWriter, r *http.Request) {
	h.scheduler.TriggerNow()
	// Return a small status snippet for HTMX to swap in.
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<span class="badge badge-info">Sync triggered — check logs below</span>`))
}

// ---- helpers ----

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	tmpl, err := template.ParseFS(templateFS, "templates/base.html", "templates/"+name)
	if err != nil {
		log.Printf("web: parse %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// qualityProfileName is a template helper that looks up a profile name by ID string.
func qualityProfileName(profiles []radarr.QualityProfile, idStr string) string {
	id, _ := strconv.Atoi(idStr)
	for _, p := range profiles {
		if p.ID == id {
			return p.Name
		}
	}
	return idStr
}
