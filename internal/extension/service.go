package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/secrets"
	"openloc/pkg/extsdk"
)

// Store is the object storage holding uploaded zips.
type Store interface {
	Put(ctx context.Context, key string, body io.Reader) error
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}

// ErrNotInstalled means the tenant has no live extension with that id.
var ErrNotInstalled = errors.New("extension not installed")

// ErrNoIcon means the extension is not installed or has no icon.
var ErrNoIcon = errors.New("extension has no icon")

// ErrCannotReview means the extension has neither the qe nor the ape
// capability.
var ErrCannotReview = errors.New("extension cannot review translations")

// ErrNotConfigured means the server has no secrets key, so no extension key
// can be read.
var ErrNotConfigured = errors.New("extensions are not configured on this server")

// ErrModuleDamaged means the stored module does not match the digest recorded
// at upload.
var ErrModuleDamaged = errors.New("stored extension module does not match its digest")

// ErrKeyUnreadable means a saved key cannot be decrypted, usually because
// the server's secrets key changed. The key has to be entered again.
var ErrKeyUnreadable = errors.New("extension key cannot be decrypted")

// InvalidPackageError is a zip or manifest the admin must fix. Its message is
// safe to show.
type InvalidPackageError struct{ Msg string }

func (e *InvalidPackageError) Error() string { return e.Msg }

// Installed is a live extension with its decoded manifest.
type Installed struct {
	Row      db.Extension
	Manifest extsdk.Manifest
}

// Provider is the AI vendor provider string of the extension.
func (i Installed) Provider() string { return Provider(i.Row.ExtID) }

// Service installs, lists and runs a tenant's extensions.
type Service struct {
	q     db.Querier
	store Store
	enc   *secrets.Encryptor
	rt    *Runtime
}

// NewService wires the extension service.
func NewService(q db.Querier, store Store, enc *secrets.Encryptor, rt *Runtime) *Service {
	return &Service{q: q, store: store, enc: enc, rt: rt}
}

// Install validates a zip, stores it and records the extension. A zip with an
// id that is already installed replaces it; keys and engines keep working.
func (s *Service) Install(ctx context.Context, tenantID int64, userID *int64, zip []byte) (Installed, error) {
	pkg, err := ReadPackage(zip)
	if err != nil {
		return Installed{}, &InvalidPackageError{Msg: err.Error()}
	}
	key := fmt.Sprintf("extensions/%d/%s/%s.zip", tenantID, pkg.Manifest.ID, pkg.SHA256)
	if err := s.store.Put(ctx, key, bytes.NewReader(zip)); err != nil {
		return Installed{}, fmt.Errorf("store extension: %w", err)
	}
	manifest, err := json.Marshal(pkg.Manifest)
	if err != nil {
		return Installed{}, fmt.Errorf("encode manifest: %w", err)
	}
	row, err := s.q.UpsertExtension(ctx, db.UpsertExtensionParams{
		TenantID: tenantID, ExtID: pkg.Manifest.ID, Name: pkg.Manifest.Name, Version: pkg.Manifest.Version,
		ApiVersion: int32(pkg.Manifest.APIVersion), Capabilities: pkg.Manifest.Capabilities,
		Manifest: manifest, StorageKey: key, Sha256: pkg.SHA256, CreatedBy: userID, Icon: pkg.Icon,
	})
	if err != nil {
		return Installed{}, fmt.Errorf("save extension: %w", err)
	}
	return Installed{Row: row, Manifest: pkg.Manifest}, nil
}

// List returns the tenant's live extensions.
func (s *Service) List(ctx context.Context, tenantID int64) ([]Installed, error) {
	rows, err := s.q.ListExtensions(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]Installed, 0, len(rows))
	for _, r := range rows {
		inst, err := decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// Get returns one live extension, or ErrNotInstalled.
func (s *Service) Get(ctx context.Context, tenantID int64, extID string) (Installed, error) {
	row, err := s.q.GetExtension(ctx, db.GetExtensionParams{TenantID: tenantID, ExtID: extID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Installed{}, ErrNotInstalled
	}
	if err != nil {
		return Installed{}, err
	}
	return decode(row)
}

// Icon returns the extension's icon PNG, or ErrNoIcon when it is not
// installed or has none.
func (s *Service) Icon(ctx context.Context, tenantID int64, extID string) ([]byte, error) {
	icon, err := s.q.GetExtensionIcon(ctx, db.GetExtensionIconParams{TenantID: tenantID, ExtID: extID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(icon) == 0) {
		return nil, ErrNoIcon
	}
	return icon, err
}

// GetByProvider returns the extension behind an "ext:" provider.
func (s *Service) GetByProvider(ctx context.Context, tenantID int64, provider string) (Installed, error) {
	id, ok := IDFromProvider(provider)
	if !ok {
		return Installed{}, ErrNotInstalled
	}
	return s.Get(ctx, tenantID, id)
}

// Delete soft-deletes an extension and deletes its keys and endpoints.
// Engines that used them read as having no key.
func (s *Service) Delete(ctx context.Context, tenantID int64, extID string) error {
	n, err := s.q.RemoveExtension(ctx, db.RemoveExtensionParams{TenantID: tenantID, ExtID: extID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotInstalled
	}
	return nil
}

func decode(row db.Extension) (Installed, error) {
	var m extsdk.Manifest
	if err := json.Unmarshal(row.Manifest, &m); err != nil {
		return Installed{}, fmt.Errorf("decode manifest of extension %d: %w", row.ID, err)
	}
	return Installed{Row: row, Manifest: m}, nil
}

// CheckKey runs the extension's check_key with a key that is not saved yet.
// Extensions without check_key accept any non-empty key.
func (s *Service) CheckKey(ctx context.Context, tenantID int64, provider, token, baseURL string) error {
	inst, err := s.GetByProvider(ctx, tenantID, provider)
	if err != nil {
		return err
	}
	baseURL = s.baseURL(inst, baseURL)
	err = s.rt.Invoke(ctx, s.call(inst, extsdk.FuncCheckKey, token, baseURL,
		extsdk.CheckKeyRequest{Context: callContext("", "", baseURL)}), nil)
	if errors.Is(err, ErrNoFunction) {
		return nil
	}
	return err
}

// BatchPredictor scores batches with one extension and one key. Implemented
// by *Predictor; the pipeline depends on this so tests can fake it.
type BatchPredictor interface {
	// Name is the extension's display name; Provider its "ext:" vendor.
	Name() string
	Provider() string
	BatchSize() int
	APE() bool
	Predict(ctx context.Context, sourceLang, targetLang string, meta extsdk.PredictMeta, rows []extsdk.PredictRow) (extsdk.PredictResponse, error)
}

// Predictor is one extension bound to one key, ready to score batches.
type Predictor struct {
	svc     *Service
	inst    Installed
	token   string
	baseURL string
}

// Name is the extension's display name.
func (p *Predictor) Name() string { return p.inst.Manifest.Name }

// Provider is the extension's AI vendor provider.
func (p *Predictor) Provider() string { return p.inst.Provider() }

// BatchSize is the most rows Predict accepts.
func (p *Predictor) BatchSize() int { return p.inst.Manifest.Limits.BatchSize }

// APE reports whether the extension may post-edit.
func (p *Predictor) APE() bool { return HasCapability(p.inst.Manifest, extsdk.CapabilityAPE) }

// Predict scores one batch.
func (p *Predictor) Predict(ctx context.Context, sourceLang, targetLang string, meta extsdk.PredictMeta, rows []extsdk.PredictRow) (extsdk.PredictResponse, error) {
	var out extsdk.PredictResponse
	if len(rows) > p.BatchSize() {
		return out, fmt.Errorf("batch of %d rows exceeds the extension's limit of %d", len(rows), p.BatchSize())
	}
	err := p.svc.rt.Invoke(ctx, p.svc.call(p.inst, extsdk.FuncPredict, p.token, p.baseURL, extsdk.PredictRequest{
		Context: callContext(sourceLang, targetLang, p.baseURL), Meta: meta, Rows: rows,
	}), &out)
	return out, err
}

// PredictorForToken resolves the extension behind a saved key. The key must
// belong to the tenant and to an installed extension with the qe capability.
func (s *Service) PredictorForToken(ctx context.Context, tenantID, tokenID int64) (BatchPredictor, error) {
	tok, err := s.q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: tokenID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("get key %d: %w", tokenID, err)
	}
	inst, err := s.GetByProvider(ctx, tenantID, tok.Provider)
	if err != nil {
		return nil, err
	}
	if !HasCapability(inst.Manifest, extsdk.CapabilityQE) && !HasCapability(inst.Manifest, extsdk.CapabilityAPE) {
		return nil, fmt.Errorf("extension %s: %w", inst.Row.ExtID, ErrCannotReview)
	}
	if s.enc == nil {
		return nil, ErrNotConfigured
	}
	token, err := s.enc.Decrypt(tok.ApiKeyEncrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt key %d: %w (%v)", tokenID, ErrKeyUnreadable, err)
	}
	return &Predictor{svc: s, inst: inst, token: strings.TrimSpace(token), baseURL: s.baseURL(inst, tok.BaseUrl)}, nil
}

// baseURL is the connection's URL, or the manifest default.
func (s *Service) baseURL(inst Installed, connURL string) string {
	if strings.TrimSpace(connURL) != "" {
		return strings.TrimSpace(connURL)
	}
	return DefaultBaseURL(inst.Manifest)
}

func (s *Service) call(inst Installed, fn, token, baseURL string, input any) Call {
	return Call{
		Manifest: inst.Manifest, SHA256: inst.Row.Sha256, Function: fn,
		Token: token, BaseURL: baseURL, Input: input,
		Wasm: func(ctx context.Context) ([]byte, error) { return s.loadWasm(ctx, inst) },
	}
}

// loadWasm downloads the zip and returns its verified module.
func (s *Service) loadWasm(ctx context.Context, inst Installed) ([]byte, error) {
	rc, err := s.store.Download(ctx, inst.Row.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("download extension %s: %w", inst.Row.ExtID, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, MaxZipBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read extension %s: %w", inst.Row.ExtID, err)
	}
	pkg, err := ReadPackage(data)
	if err != nil {
		return nil, fmt.Errorf("extension %s: %w", inst.Row.ExtID, err)
	}
	if pkg.SHA256 != inst.Row.Sha256 {
		return nil, fmt.Errorf("extension %s: %w", inst.Row.ExtID, ErrModuleDamaged)
	}
	return pkg.Wasm, nil
}

func callContext(sl, tl, baseURL string) extsdk.Context {
	c := extsdk.Context{SourceLang: sl, TargetLang: tl}
	if baseURL != "" {
		c.Settings = map[string]string{extsdk.SettingBaseURL: baseURL}
	}
	return c
}
