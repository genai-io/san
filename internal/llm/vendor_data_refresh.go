package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/internal/confdir"
)

// Keeping the model data current: models.dev, fetched daily, cut down to the
// vendors San reaches and the fields it reads.

const (
	modelsDevURL = "https://models.dev/api.json"

	// modelDataMaxAge is how long a fetched copy stays current. Vendors
	// change prices and lineups in weeks, not hours.
	modelDataMaxAge = 24 * time.Hour

	// modelDataTimeout bounds one fetch; it runs off the startup path.
	modelDataTimeout = 30 * time.Second

	// modelDataMaxBytes caps the download: the full listing is ~5 MB.
	modelDataMaxBytes = 64 << 20
)

// modelsDevSource names the models.dev provider a catalog vendor's data comes
// from, and — for a provider that also resells other vendors' models — the
// ID prefix that picks out the ones this vendor serves.
type modelsDevSource struct {
	provider string
	prefix   string
}

// modelsDevSources maps catalog vendor IDs to models.dev providers. A vendor
// missing here has no public data: Ollama serves what was pulled locally, and
// the ChatGPT subscription publishes its own lineup.
var modelsDevSources = map[string]modelsDevSource{
	"anthropic":        {provider: "anthropic"},
	"anthropic-vertex": {provider: "google-vertex-anthropic"},
	"openai":           {provider: "openai"},
	"copilot":          {provider: "github-copilot"},
	"google":           {provider: "google"},
	"google-vertex":    {provider: "google-vertex", prefix: "gemini"},
	"deepseek":         {provider: "deepseek"},
	"sensenova":        {provider: "sensenova"},
	"minimax":          {provider: "minimax-cn"},
	"moonshot":         {provider: "moonshotai-cn"},
	"alibaba":          {provider: "alibaba-cn"},
	"bigmodel":         {provider: "zhipuai"},
	"mimo":             {provider: "xiaomi"},
	"volcengine":       {provider: "volcengine"},
	"agnesai":          {provider: "agnes"},
}

// modelDataCachePath is where the daily copy lives.
func modelDataCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(confdir.Dir(home), "cache", "models.json")
}

// RefreshModelData fetches models.dev into the cache when the cached copy is
// missing or older than a day. The new data takes effect on the next start.
func RefreshModelData(ctx context.Context) error {
	path := modelDataCachePath()
	if path == "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < modelDataMaxAge {
		return nil
	}
	data, err := fetchModelsDev(ctx, modelsDevURL)
	if err != nil {
		return err
	}
	return writeLineups(path, data)
}

// fetchModelsDev downloads models.dev and keeps San's share of it.
func fetchModelsDev(ctx context.Context, url string) (lineups, error) {
	ctx, cancel := context.WithTimeout(ctx, modelDataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: models.dev answered http %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, modelDataMaxBytes))
	if err != nil {
		return nil, err
	}
	return trimModelsDev(raw)
}

// trimModelsDev re-keys models.dev by catalog vendor ID and drops what San
// does not read: vendors it does not reach, models an agent cannot drive, and
// every field outside modelSpec — endpoints included. Figures outside any
// plausible range are dropped rather than trusted.
func trimModelsDev(raw []byte) (lineups, error) {
	var providers map[string]struct {
		Models map[string]modelSpec `json:"models"`
	}
	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, fmt.Errorf("llm: reading models.dev: %w", err)
	}
	out := make(lineups, len(modelsDevSources))
	for vendorID, source := range modelsDevSources {
		models := make(map[string]modelSpec)
		for id, spec := range providers[source.provider].Models {
			// Vertex publishes each model under an "@default" alias; the
			// dateless ID is what San asks for.
			id = strings.ToLower(strings.TrimSuffix(id, "@default"))
			if !strings.HasPrefix(id, source.prefix) || !spec.agentUsable() {
				continue
			}
			models[id] = spec.plausible()
		}
		if len(models) > 0 {
			out[vendorID] = lineup{Models: models}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("llm: models.dev listed none of San's vendors")
	}
	return out, nil
}

// plausible clears a figure no model has: a window past 100M tokens, a price
// past $10,000 per million. Wrong data should read as unknown, not as fact.
func (s modelSpec) plausible() modelSpec {
	if s.Limit != nil && (s.Limit.Context < 0 || s.Limit.Context > 100_000_000 ||
		s.Limit.Output < 0 || s.Limit.Output > 100_000_000) {
		s.Limit = nil
	}
	if s.Cost != nil {
		for _, rate := range []float64{s.Cost.Input, s.Cost.Output, s.Cost.CacheRead, s.Cost.CacheWrite} {
			if rate < 0 || rate > 10_000 {
				s.Cost = nil
				break
			}
		}
	}
	return s
}

// writeLineups replaces the file in one rename, so a reader never sees half
// of it.
func writeLineups(path string, data lineups) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
