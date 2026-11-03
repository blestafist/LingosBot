package webui

import "net/http"

func (x *app) homeData(w http.ResponseWriter, r *http.Request, csrf string) (map[string]any, bool) {
	saved := false
	if csrf != "" {
		if err := x.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM credentials WHERE id=1)`).Scan(&saved); err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return nil, false
		}
	}
	return map[string]any{
		"In":               csrf != "",
		"CSRF":             csrf,
		"LoginError":       r.URL.Query().Get("error") == "1",
		"CredentialsSaved": saved,
	}, true
}
