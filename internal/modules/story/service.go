package story

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/storytranslation"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// TranslationLang is the only machine-translation target for now.
const TranslationLang = "zh-Hans"

// failedRetryAfter keeps a failed episode out of the queue for a while.
const failedRetryAfter = 6 * time.Hour

// maxFailedAttempts stops retrying an episode that keeps failing.
const maxFailedAttempts = 5

// Service owns the JP asset index and the translation worker.
type Service struct {
	cfg    config.StoryConfig
	db     *postgresql.Client
	logger *harukiLogger.Logger
	fetch  *fetcher
	index  *indexBuilder // nil when the index is not configured
	tr     *translator   // nil when translation is disabled
	gloss  glossaryCache
	kick   chan struct{}

	mu      sync.RWMutex
	catalog *Catalog
	current string // scenario being translated
}

// New validates the configuration; nil means the feature is off.
func New(cfg config.StoryConfig, db *postgresql.Client, logger *harukiLogger.Logger) (*Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RegistryBaseURL == "" || cfg.AssetBaseURL == "" {
		return nil, fmt.Errorf("story: registry_base_url and asset_base_url are required")
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "Haruki-Toolbox-Backend"
	}
	httpClient := &http.Client{Timeout: 2 * time.Minute}
	s := &Service{
		cfg:    cfg,
		db:     db,
		logger: logger,
		fetch:  newFetcher(cfg.RegistryBaseURL, cfg.AssetBaseURL, ua, httpClient),
		kick:   make(chan struct{}, 1),
	}
	if ix := cfg.Index; ix.S3Endpoint != "" && ix.AccessKeyID != "" && ix.SecretAccessKey != "" {
		region := ix.S3Region
		if region == "" {
			region = "garage"
		}
		bucket := ix.Bucket
		if bucket == "" {
			bucket = "pjsk-assets"
		}
		prefix := ix.Prefix
		if prefix == "" {
			prefix = "jp-assets/"
		}
		s.index = &indexBuilder{
			prefix: prefix,
			lister: &s3Lister{
				endpoint: strings.TrimRight(ix.S3Endpoint, "/"), region: region, bucket: bucket,
				accessKey: ix.AccessKeyID, secretKey: ix.SecretAccessKey,
				client: &http.Client{Timeout: time.Minute}, now: time.Now,
			},
		}
	}
	if tc := cfg.Translation; tc.Enabled {
		if tc.LLMBaseURL == "" || tc.LLMAPIKey == "" || tc.LLMModel == "" {
			return nil, fmt.Errorf("story: translation needs llm_base_url, llm_api_key and llm_model")
		}
		timeout := time.Duration(tc.LLMTimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 15 * time.Minute
		}
		s.tr = &translator{
			llm: &llmClient{
				baseURL: tc.LLMBaseURL, apiKey: tc.LLMAPIKey, model: tc.LLMModel, timeout: timeout,
				client: &http.Client{}, retries: 3, backoff: 30 * time.Second,
			},
			chunkLines: tc.ChunkLines,
		}
	}
	return s, nil
}

// Start launches the background loops and returns a wait function to call
// after ctx is cancelled.
func (s *Service) Start(ctx context.Context) func() {
	var wg sync.WaitGroup
	if s.index != nil {
		every := time.Duration(s.cfg.Index.RefreshMinutes) * time.Minute
		if every <= 0 {
			every = time.Hour
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.loop(ctx, every, nil, func() {
				if err := s.index.refresh(ctx); err != nil {
					s.logger.Warnf("story index refresh failed: %v", err)
					return
				}
				idx, _ := s.index.snapshot()
				s.logger.Infof("story index refreshed: %d models, %d movies, version %s", len(idx.Models), len(idx.Movies), idx.Version)
			})
		}()
	}
	every := time.Duration(s.cfg.Translation.IntervalMinutes) * time.Minute
	if every <= 0 {
		every = 30 * time.Minute
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.loop(ctx, every, s.kick, func() { s.translateRun(ctx) })
	}()
	return wg.Wait
}

func (s *Service) loop(ctx context.Context, every time.Duration, kick <-chan struct{}, run func()) {
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
		run()
	}
}

// Kick asks the translation loop for an early pass.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) refreshCatalog(ctx context.Context) (*Catalog, error) {
	c, err := buildCatalog(ctx, s.fetch)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.catalog = c
	s.mu.Unlock()
	return c, nil
}

func (s *Service) currentCatalog() *Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}

// translateRun refreshes the catalog and translates pending JP-only episodes.
func (s *Service) translateRun(ctx context.Context) {
	c, err := s.refreshCatalog(ctx)
	if err != nil {
		s.logger.Warnf("story catalog refresh failed: %v", err)
		return
	}
	if s.tr == nil {
		return
	}
	rows, err := s.db.StoryTranslation.Query().
		Where(storytranslation.Lang(TranslationLang)).
		Select(storytranslation.FieldScenarioID, storytranslation.FieldStatus, storytranslation.FieldAttempts, storytranslation.FieldUpdatedAt).
		All(ctx)
	if err != nil {
		s.logger.Warnf("story translation query failed: %v", err)
		return
	}
	skip := map[string]bool{}
	for _, r := range rows {
		switch {
		case r.Status == storytranslation.StatusDone:
			skip[r.ScenarioID] = true
		case r.Attempts >= maxFailedAttempts, time.Since(r.UpdatedAt) < failedRetryAfter:
			skip[r.ScenarioID] = true
		}
	}
	pending := c.pendingOrder(func(id string) bool { return skip[id] })
	if len(pending) == 0 {
		return
	}
	limit := s.cfg.Translation.MaxEpisodesPerRun
	thisRun := len(pending)
	if limit > 0 && limit < thisRun {
		thisRun = limit
	}
	s.logger.Infof("story translation: %d JP-only episodes pending (this run: %d)", len(pending), thisRun)
	done := 0
	for _, ep := range pending {
		if ctx.Err() != nil || (limit > 0 && done >= limit) {
			return
		}
		if err := s.translateEpisode(ctx, c, ep); err != nil {
			s.logger.Warnf("story translation %s failed: %v", ep.ScenarioID, err)
		}
		done++
	}
}

func (s *Service) translateEpisode(ctx context.Context, c *Catalog, ep *Episode) error {
	s.mu.Lock()
	s.current = ep.ScenarioID
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.current = ""
		s.mu.Unlock()
	}()

	var scn *Scenario
	for _, p := range ep.Paths {
		got, err := s.fetch.scenario(ctx, "jp", p)
		if err != nil {
			return err
		}
		if got != nil {
			scn = got
			break
		}
	}
	if scn == nil {
		return nil // not exported yet; picked up by a later run
	}
	gloss, err := s.gloss.get(ctx, s.fetch)
	if err != nil {
		return err
	}
	siblings := c.Series[ep.SeriesKey]
	var summaries []string
	for _, sib := range siblings {
		if sib.No >= ep.No {
			break
		}
		row, err := s.db.StoryTranslation.Query().
			Where(storytranslation.ScenarioID(sib.ScenarioID), storytranslation.Lang(TranslationLang), storytranslation.StatusEQ(storytranslation.StatusDone)).
			Only(ctx)
		if err == nil && row.Summary != "" {
			summaries = append(summaries, fmt.Sprintf("%s「%s」：%s", sib.Label, sib.Title, row.Summary))
		}
	}
	started := time.Now()
	res, terr := s.tr.translate(ctx, scn, promptContext{Episode: ep, Siblings: siblings, Summaries: summaries, Glossary: gloss})
	if terr != nil {
		return s.saveFailure(ctx, ep.ScenarioID, terr)
	}
	if err := s.save(ctx, ep.ScenarioID, sourceHash(scn), res); err != nil {
		return err
	}
	s.logger.Infof("story translation %s done: %d lines in %s (%d+%d tokens)", ep.ScenarioID, len(res.Lines), time.Since(started).Round(time.Second), res.Usage.PromptTokens, res.Usage.CompletionTokens)
	return nil
}

func (s *Service) save(ctx context.Context, id, hash string, res *translation) error {
	existing, err := s.db.StoryTranslation.Query().
		Where(storytranslation.ScenarioID(id), storytranslation.Lang(TranslationLang)).Only(ctx)
	if err != nil && !postgresql.IsNotFound(err) {
		return err
	}
	if existing != nil {
		return existing.Update().
			SetStatus(storytranslation.StatusDone).SetSourceHash(hash).SetModel(s.cfg.Translation.LLMModel).
			SetNames(res.Names).SetLines(res.Lines).SetEffects(res.Effects).SetSummary(res.Summary).SetError("").
			AddAttempts(1).SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
			Exec(ctx)
	}
	return s.db.StoryTranslation.Create().
		SetScenarioID(id).SetLang(TranslationLang).SetSourceRegion("jp").SetSourceHash(hash).
		SetStatus(storytranslation.StatusDone).SetModel(s.cfg.Translation.LLMModel).
		SetNames(res.Names).SetLines(res.Lines).SetEffects(res.Effects).SetSummary(res.Summary).SetAttempts(1).
		SetPromptTokens(res.Usage.PromptTokens).SetCompletionTokens(res.Usage.CompletionTokens).
		Exec(ctx)
}

func (s *Service) saveFailure(ctx context.Context, id string, cause error) error {
	msg := cause.Error()
	if len(msg) > 1900 {
		msg = msg[:1900]
	}
	existing, err := s.db.StoryTranslation.Query().
		Where(storytranslation.ScenarioID(id), storytranslation.Lang(TranslationLang)).Only(ctx)
	switch {
	case err == nil && existing.Status == storytranslation.StatusDone:
		// keep the old good translation; only note the error
		_ = existing.Update().SetError(msg).Exec(ctx)
	case err == nil:
		_ = existing.Update().SetError(msg).AddAttempts(1).Exec(ctx)
	case postgresql.IsNotFound(err):
		_ = s.db.StoryTranslation.Create().SetScenarioID(id).SetLang(TranslationLang).
			SetStatus(storytranslation.StatusFailed).SetError(msg).SetAttempts(1).Exec(ctx)
	}
	return cause
}
