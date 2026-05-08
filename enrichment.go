package onairlogsync

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/oauth2/google"
)

// Vertex AI model used for the verification step. gemini-3.1-flash-lite
// is the cheapest GA model and is good enough for "is candidate X the
// same song as the input?" type judgements.
const vertexModel = "gemini-3.1-flash-lite"

// Default freshness window — songs enriched more recently than this are
// not re-enriched on subsequent airplays. 30 days lets us pick up new
// canonical info as it appears in iTunes without hammering the API.
const enrichmentFreshness = 30 * 24 * time.Hour

// EnrichmentResult is what the pipeline produces for one (rawTitle,
// rawArtist) pair.
type EnrichmentResult struct {
	ITunesTrackID   int64
	CanonicalTitle  string
	CanonicalArtist string
	CanonicalKey    string
	Confidence      float64
	ITunesResponse  map[string]interface{}
	LLMResponse     map[string]interface{}
}

// Enrich consults the iTunes Search API and Gemini Flash to resolve a
// raw (title, artist) pair to a canonical form. Failures are
// non-fatal; the caller decides whether to persist whatever partial
// result came back.
func (app *App) Enrich(ctx context.Context, rawTitle, rawArtist string) (*EnrichmentResult, error) {
	itunesRaw, candidates, err := iTunesSearch(ctx, rawTitle, rawArtist)
	if err != nil {
		return nil, fmt.Errorf("itunes search: %w", err)
	}

	verdict, llmRaw, err := app.geminiVerify(ctx, rawTitle, rawArtist, candidates)
	if err != nil {
		return nil, fmt.Errorf("gemini verify: %w", err)
	}

	res := &EnrichmentResult{
		ITunesResponse: itunesRaw,
		LLMResponse:    llmRaw,
	}
	if v := verdict["trackId"]; v != nil {
		switch tv := v.(type) {
		case float64:
			res.ITunesTrackID = int64(tv)
		case string:
			if n, err := strconv.ParseInt(tv, 10, 64); err == nil {
				res.ITunesTrackID = n
			}
		}
	}
	if s, _ := verdict["canonicalTitle"].(string); s != "" {
		res.CanonicalTitle = s
	}
	if s, _ := verdict["canonicalArtist"].(string); s != "" {
		res.CanonicalArtist = s
	}
	if c, ok := verdict["confidence"].(float64); ok {
		res.Confidence = c
	}

	// Canonical key: prefer the verified iTunes track id, fall back to
	// the canonical (title, artist) hash so soft-link merging still
	// has something to group on for tracks iTunes can't identify.
	switch {
	case res.ITunesTrackID > 0:
		res.CanonicalKey = "itunes:" + strconv.FormatInt(res.ITunesTrackID, 10)
	case res.CanonicalTitle != "" && res.CanonicalArtist != "":
		h := sha1.New()
		fmt.Fprintf(h, "%s\x00%s", Normalize(res.CanonicalTitle), Normalize(res.CanonicalArtist))
		res.CanonicalKey = "name:" + hex.EncodeToString(h.Sum(nil))
	}
	return res, nil
}

// iTunesSearch returns the raw response (for archival) and a parsed
// candidate list (for the LLM prompt).
func iTunesSearch(ctx context.Context, title, artist string) (map[string]interface{}, []map[string]interface{}, error) {
	q := url.Values{
		"term":    {title + " " + artist},
		"country": {"jp"},
		"media":   {"music"},
		"entity":  {"song"},
		"limit":   {"5"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://itunes.apple.com/search?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	cli := &http.Client{Timeout: 10 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("itunes status %d: %s", resp.StatusCode, body)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, err
	}
	results, _ := raw["results"].([]interface{})
	cands := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		if m, ok := r.(map[string]interface{}); ok {
			cands = append(cands, map[string]interface{}{
				"trackId":        m["trackId"],
				"trackName":      m["trackName"],
				"artistName":     m["artistName"],
				"collectionName": m["collectionName"],
			})
		}
	}
	raw["queriedAt"] = time.Now().UTC().Format(time.RFC3339)
	return raw, cands, nil
}

func (app *App) geminiVerify(ctx context.Context, rawTitle, rawArtist string, candidates []map[string]interface{}) (map[string]interface{}, map[string]interface{}, error) {
	candJSON, _ := json.MarshalIndent(candidates, "", "  ")
	prompt := fmt.Sprintf(
		"You match a raw radio playlog entry to one of the iTunes candidates if and only if they refer to the same recording (or the same song; album/version differences are OK). "+
			"Account for typos, the U+FFFD replacement char, abbreviations like II/2, hiragana/katakana variation, "+
			"and English/Japanese transliteration of artist names (e.g. ALETHA FRANKLIN ↔ アレサ・フランクリン).\n\n"+
			"INPUT raw_title : %q\n"+
			"INPUT raw_artist: %q\n\n"+
			"CANDIDATES (JSON):\n%s\n\n"+
			`Reply with strict JSON only: {"trackId": <int or null>, "canonicalTitle": "<string or null>", "canonicalArtist": "<string or null>", "confidence": <0..1>, "reason": "<short>"}.`+"\n"+
			"If none match, set trackId=null but still fill canonicalTitle/canonicalArtist with your best guess of the correct surface form.",
		rawTitle, rawArtist, string(candJSON),
	)

	body := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"role": "user", "parts": []map[string]interface{}{{"text": prompt}}},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
			"temperature":      0,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	respBody, err := app.callVertexAI(ctx, payload)
	if err != nil {
		return nil, nil, err
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode vertex resp: %w; body=%s", err, respBody)
	}

	verdict := extractVerdict(resp)
	llmArchive := map[string]interface{}{
		"queriedAt": time.Now().UTC().Format(time.RFC3339),
		"model":     vertexModel,
		"verdict":   verdict,
	}
	if u, ok := resp["usageMetadata"].(map[string]interface{}); ok {
		llmArchive["usageMetadata"] = u
	}
	return verdict, llmArchive, nil
}

func extractVerdict(resp map[string]interface{}) map[string]interface{} {
	cands, _ := resp["candidates"].([]interface{})
	if len(cands) == 0 {
		return nil
	}
	c, _ := cands[0].(map[string]interface{})
	content, _ := c["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})
	if len(parts) == 0 {
		return nil
	}
	p0, _ := parts[0].(map[string]interface{})
	text, _ := p0["text"].(string)
	if text == "" {
		return nil
	}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return map[string]interface{}{"_raw": text}
	}
	return v
}

func (app *App) callVertexAI(ctx context.Context, payload []byte) ([]byte, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("token source: %w", err)
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}

	endpoint := fmt.Sprintf(
		"https://aiplatform.googleapis.com/v1/projects/%s/locations/global/publishers/google/models/%s:generateContent",
		app.ProjectID(), vertexModel,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("X-Goog-User-Project", app.ProjectID())
	req.Header.Set("Content-Type", "application/json")

	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("vertex status %d: %s", resp.StatusCode, body)
	}
	return body, nil
}
