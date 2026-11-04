package webui

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type vocabularyTopic struct {
	Info struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ExpectedCount *int   `json:"expected_count"`
	} `json:"info"`
	Entries []struct {
		Word        string `json:"word"`
		Translation string `json:"translation"`
	} `json:"entries"`
}

type scrapedClass struct {
	Class    cachedClass       `json:"class"`
	Wordsets []vocabularyTopic `json:"wordsets"`
}

func initLibraryDB(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS library_classes (
		user_id INTEGER NOT NULL REFERENCES users(id),class_id TEXT NOT NULL,title TEXT NOT NULL,
		collected_at INTEGER NOT NULL,PRIMARY KEY(user_id,class_id));
		CREATE TABLE IF NOT EXISTS library_topics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL,class_id TEXT NOT NULL,
		name TEXT NOT NULL,count INTEGER NOT NULL,data TEXT NOT NULL,
		FOREIGN KEY(user_id,class_id) REFERENCES library_classes(user_id,class_id) ON DELETE CASCADE);
		CREATE INDEX IF NOT EXISTS topics_class ON library_topics(user_id,class_id);`)
	return err
}

func (x *app) saveVocabulary(userID int64, requestedID string, raw json.RawMessage) error {
	var scrape scrapedClass
	if err := json.Unmarshal(raw, &scrape); err != nil {
		return errors.New("invalid vocabulary result")
	}
	if scrape.Class.GroupID != requestedID || scrape.Wordsets == nil {
		return errors.New("vocabulary class does not match the requested class")
	}
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO library_classes(user_id,class_id,title,collected_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id,class_id) DO UPDATE SET title=excluded.title,collected_at=excluded.collected_at`, userID, requestedID, scrape.Class.Title, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM library_topics WHERE user_id=? AND class_id=?`, userID, requestedID); err != nil {
		return err
	}
	for _, topic := range scrape.Wordsets {
		data, err := json.Marshal(topic)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO library_topics(user_id,class_id,name,count,data) VALUES(?,?,?,?,?)`, userID, requestedID, topic.Info.Name, len(topic.Entries), string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (x *app) libraryAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	type topic struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	type class struct {
		ClassID     string  `json:"class_id"`
		Title       string  `json:"title"`
		CollectedAt int64   `json:"collected_at"`
		Topics      []topic `json:"topics"`
	}
	rows, err := x.db.Query(`SELECT c.class_id,c.title,c.collected_at,t.id,t.name,t.count
		FROM library_classes c LEFT JOIN library_topics t ON c.user_id=t.user_id AND c.class_id=t.class_id
		WHERE c.user_id=? ORDER BY c.title COLLATE NOCASE,c.class_id,t.id`, u.ID)
	if err != nil {
		apiError(w, 500, "Could not load your library.")
		return
	}
	defer rows.Close()
	classes := []class{}
	for rows.Next() {
		var c class
		var id sql.NullInt64
		var name sql.NullString
		var count sql.NullInt64
		if err := rows.Scan(&c.ClassID, &c.Title, &c.CollectedAt, &id, &name, &count); err != nil {
			apiError(w, 500, "Could not read your library.")
			return
		}
		if len(classes) == 0 || classes[len(classes)-1].ClassID != c.ClassID {
			c.Topics = []topic{}
			classes = append(classes, c)
		}
		if id.Valid {
			last := &classes[len(classes)-1]
			last.Topics = append(last.Topics, topic{id.Int64, name.String, int(count.Int64)})
		}
	}
	if rows.Err() != nil {
		apiError(w, 500, "Could not read your library.")
		return
	}
	writeJSON(w, 200, map[string]any{"classes": classes})
}

func (x *app) topicAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		apiError(w, 404, "Topic not found.")
		return
	}
	var data string
	var title, class string
	err = x.db.QueryRow(`SELECT t.data,c.title,c.class_id FROM library_topics t JOIN library_classes c
		ON t.user_id=c.user_id AND t.class_id=c.class_id WHERE t.id=? AND t.user_id=?`, id, u.ID).Scan(&data, &title, &class)
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, "Topic not found.")
		return
	} else if err != nil {
		apiError(w, 500, "Could not load this topic.")
		return
	}
	writeJSON(w, 200, map[string]any{"topic": json.RawMessage(data), "class_title": title, "class_id": class})
}

func (x *app) exportAPI(w http.ResponseWriter, r *http.Request) {
	u, ok := x.apiUser(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	var topics []int64
	if r.Method == http.MethodPost {
		if r.URL.RawQuery != "" {
			apiError(w, 400, "Send export options in the JSON body only.")
			return
		}
		var body struct {
			Format string  `json:"format"`
			Topics []int64 `json:"topics"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if len(body.Topics) < 1 || len(body.Topics) > 500 {
			apiError(w, 400, "Choose between 1 and 500 topics.")
			return
		}
		seen := make(map[int64]bool, len(body.Topics))
		for _, id := range body.Topics {
			if id < 1 {
				apiError(w, 400, "Invalid topic.")
				return
			}
			if !seen[id] {
				topics = append(topics, id)
				seen[id] = true
			}
		}
		format = body.Format
	}
	if format != "json" && format != "md" {
		apiError(w, 400, "Choose Markdown or JSON.")
		return
	}
	class, topic := r.URL.Query().Get("class"), r.URL.Query().Get("topic")
	if class != "" && topic != "" {
		apiError(w, 400, "Choose either one class or one topic.")
		return
	}
	query := `SELECT c.title,c.class_id,t.data FROM library_classes c LEFT JOIN library_topics t
		ON t.user_id=c.user_id AND t.class_id=c.class_id WHERE c.user_id=?`
	args := []any{u.ID}
	if class != "" {
		query += ` AND c.class_id=?`
		args = append(args, class)
	}
	if topic != "" {
		id, err := strconv.ParseInt(topic, 10, 64)
		if err != nil || id < 1 {
			apiError(w, 400, "Invalid topic.")
			return
		}
		query += ` AND t.id=?`
		args = append(args, id)
	}
	if len(topics) > 0 {
		query += ` AND t.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(topics)), ",") + `)`
		for _, id := range topics {
			args = append(args, id)
		}
	}
	query += ` ORDER BY c.title COLLATE NOCASE,c.class_id,t.id`
	rows, err := x.db.Query(query, args...)
	if err != nil {
		apiError(w, 500, "Could not export vocabulary.")
		return
	}
	defer rows.Close()
	classes := []scrapedClass{}
	topicCount := 0
	for rows.Next() {
		var c cachedClass
		var data sql.NullString
		if err := rows.Scan(&c.Title, &c.GroupID, &data); err != nil {
			apiError(w, 500, "Could not read vocabulary.")
			return
		}
		if len(classes) == 0 || classes[len(classes)-1].Class.GroupID != c.GroupID {
			classes = append(classes, scrapedClass{Class: c, Wordsets: []vocabularyTopic{}})
		}
		if data.Valid {
			var topic vocabularyTopic
			if err := json.Unmarshal([]byte(data.String), &topic); err != nil {
				apiError(w, 500, "Stored vocabulary could not be read.")
				return
			}
			last := &classes[len(classes)-1]
			last.Wordsets = append(last.Wordsets, topic)
			topicCount++
		}
	}
	if rows.Err() != nil {
		apiError(w, 500, "Could not read vocabulary.")
		return
	}
	if len(classes) == 0 || len(topics) > 0 && topicCount != len(topics) {
		apiError(w, 404, "No saved vocabulary found for this selection.")
		return
	}
	// Fixed filenames cannot inject headers, path components or unsafe extensions.
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="vocabulary.%s"`, format))
	if format == "json" {
		writeJSON(w, 200, map[string]any{"classes": classes})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	var out strings.Builder
	out.WriteString("# Vocabulary\n")
	for _, c := range classes {
		fmt.Fprintf(&out, "\n## %s\n\nClass ID: %s\n", markdownText(c.Class.Title), c.Class.GroupID)
		for _, topic := range c.Wordsets {
			fmt.Fprintf(&out, "\n### %s\n\n| Word | Translation |\n| --- | --- |\n", markdownText(topic.Info.Name))
			for _, e := range topic.Entries {
				fmt.Fprintf(&out, "| %s | %s |\n", markdownText(e.Word), markdownText(e.Translation))
			}
		}
	}
	if _, err := w.Write([]byte(out.String())); err != nil {
		return
	}
}

func markdownText(s string) string {
	s = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", " ", "\n", " ").Replace(s)
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "#", "\\#", "!", "\\!").Replace(s)
}
