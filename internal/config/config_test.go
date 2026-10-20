// Pins that bool env vars (RUN_SCHEDULER and any other env using
// parseBoolOr) accept every strconv.ParseBool form, so RUN_SCHEDULER=False
// turns the scheduler off.

package config

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseBoolOr_AcceptsAllStandardForms(t *testing.T) {
	cases := []struct {
		envValue string
		want     bool
	}{
		// Truthy
		{"1", true},
		{"t", true},
		{"T", true},
		{"true", true},
		{"True", true},
		{"TRUE", true},
		// Falsy
		{"0", false},
		{"f", false},
		{"F", false},
		{"false", false},
		{"False", false},
		{"FALSE", false},
	}
	for _, c := range cases {
		t.Run(c.envValue, func(t *testing.T) {
			t.Setenv("FOO_BOOL", c.envValue)
			// fallback is the OPPOSITE of want, so a no-op parser would
			// return the wrong value — anti-tautology baked in.
			got := parseBoolOr("FOO_BOOL", !c.want)
			if got != c.want {
				t.Errorf("parseBoolOr(%q) = %t, want %t", c.envValue, got, c.want)
			}
		})
	}
}

func TestParseBoolOr_FallsBackOnEmpty(t *testing.T) {
	t.Setenv("FOO_BOOL", "")
	if got := parseBoolOr("FOO_BOOL", true); got != true {
		t.Errorf("empty env should use fallback (true), got %t", got)
	}
	if got := parseBoolOr("FOO_BOOL", false); got != false {
		t.Errorf("empty env should use fallback (false), got %t", got)
	}
}

func TestParseBoolOr_FallsBackOnGarbage(t *testing.T) {
	// Operator typo like "yes" or "on" — strconv.ParseBool rejects these.
	// We log + fall back rather than silently inheriting the default,
	// so the warning line tells ops their value didn't take.
	t.Setenv("FOO_BOOL", "yes")
	if got := parseBoolOr("FOO_BOOL", true); got != true {
		t.Errorf("garbage env should use fallback, got %t", got)
	}
}

// Error reporting needs both HONEYBADGER_ENABLED=true and the key. Staging
// mounts the same key as production, so the switch alone keeps it silent.
// Blanking the key in Load disables backend notices, the browser proxy, AND
// the /api/config error_reporting flag in one move. ENV plays no part.
func TestLoad_HoneybadgerNeedsSwitchAndKey(t *testing.T) {
	for _, c := range []struct{ name, env, enabled, key, want string }{
		{"switch and key", "production", "true", "hb-test-key", "hb-test-key"},
		{"switch and key outside production", "staging", "true", "hb-test-key", "hb-test-key"},
		{"switch without key", "production", "true", "", ""},
		{"key without switch in production", "production", "", "hb-test-key", ""},
		{"key with switch off", "production", "false", "hb-test-key", ""},
		{"key with garbage switch", "production", "yes-please", "hb-test-key", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ENV", c.env)
			t.Setenv("HONEYBADGER_ENABLED", c.enabled)
			t.Setenv("HONEYBADGER_API_KEY", c.key)
			if got := Load().HoneybadgerAPIKey; got != c.want {
				t.Errorf("HoneybadgerAPIKey = %q, want %q", got, c.want)
			}
		})
	}
}

// DEMO_ORG defaults on everywhere but production, and the variable wins either
// way.
func TestLoad_DemoOrgDefault(t *testing.T) {
	for _, c := range []struct {
		env, flag string
		want      bool
	}{
		{"development", "", true},
		{"staging", "", true},
		{"production", "", false},
		{"production", "true", true},
		{"development", "false", false},
	} {
		t.Run(c.env+"/"+c.flag, func(t *testing.T) {
			t.Setenv("ENV", c.env)
			t.Setenv("DEMO_ORG", c.flag)
			if got := Load().DemoOrg; got != c.want {
				t.Errorf("ENV=%s DEMO_ORG=%q: DemoOrg = %v, want %v", c.env, c.flag, got, c.want)
			}
		})
	}
}

// AI_SECRETS_KEY falls back to the dev key only in development. Anywhere else
// a missing key is a startup error, because the fallback is a public string.
func TestLoad_AISecretsKeyRequiredOutsideDevelopment(t *testing.T) {
	for _, env := range []string{"production", "staging", "prod"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("ENV", env)
			t.Setenv("AI_SECRETS_KEY", "")
			cfg := Load()
			if cfg.AISecretsKey != "" {
				t.Errorf("AISecretsKey = %q, want empty (no fallback outside development)", cfg.AISecretsKey)
			}
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() = nil, want an error for a missing AI_SECRETS_KEY")
			}
		})
	}
}

func TestLoad_AISecretsKeySetIsAccepted(t *testing.T) {
	for _, key := range []string{"a-real-key", DevAISecretsKey} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("ENV", "production")
			t.Setenv("AI_SECRETS_KEY", key)
			t.Setenv("JWT_SECRET", strongJWTSecret)
			cfg := Load()
			if cfg.AISecretsKey != key {
				t.Errorf("AISecretsKey = %q, want %q", cfg.AISecretsKey, key)
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestLoad_AISecretsKeyDevFallback(t *testing.T) {
	t.Setenv("ENV", "")
	t.Setenv("AI_SECRETS_KEY", "")
	cfg := Load()
	if cfg.AISecretsKey != DevAISecretsKey {
		t.Errorf("AISecretsKey = %q, want the dev fallback %q", cfg.AISecretsKey, DevAISecretsKey)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil in development", err)
	}
}

// The localhost defaults are the dev stack's ports (the Makefile's
// *_PORT values), so a bare ./server reaches `make dev`'s services and no two
// services share a port.
func TestLoad_LocalDefaultsUseTheDevStackPorts(t *testing.T) {
	for _, k := range []string{"DATABASE_URL", "REDIS_ADDR", "S3_ENDPOINT", "BASE_URL", "PARSER_SIDECAR_URL", "SPELLCHECK_LT_URL"} {
		t.Setenv(k, "")
	}
	cfg := Load()
	want := map[string]struct{ got, port string }{
		"BASE_URL":           {cfg.BaseURL, "9000"},
		"DATABASE_URL":       {cfg.DatabaseURL, "9002"},
		"REDIS_ADDR":         {"redis://" + cfg.RedisAddr, "9003"},
		"S3_ENDPOINT":        {cfg.S3Endpoint, "9004"},
		"PARSER_SIDECAR_URL": {cfg.ParserSidecarURL, "9007"},
		"SPELLCHECK_LT_URL":  {cfg.SpellcheckLTURL, "9008"},
	}
	seen := map[string]string{}
	for name, w := range want {
		u, err := url.Parse(w.got)
		if err != nil || !strings.HasPrefix(u.Host, "localhost:") {
			t.Fatalf("%s default %q is not a localhost URL", name, w.got)
		}
		if u.Port() != w.port {
			t.Errorf("%s default %q: port %s, want the dev stack's %s", name, w.got, u.Port(), w.port)
		}
		if other, dup := seen[u.Port()]; dup {
			t.Errorf("%s and %s both default to port %s", name, other, u.Port())
		}
		seen[u.Port()] = name
	}
}

func TestLoad_SMTPSettings(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "s3cret")
	t.Setenv("SMTP_TLS", "tls")
	c := Load()
	if c.SMTPHost != "smtp.example.com" || c.SMTPPort != 2525 || c.SMTPUsername != "mailer" || c.SMTPPassword != "s3cret" || c.SMTPTLS != "tls" {
		t.Errorf("SMTP settings = %q %d %q %q %q", c.SMTPHost, c.SMTPPort, c.SMTPUsername, c.SMTPPassword, c.SMTPTLS)
	}

	t.Setenv("SMTP_PORT", "")
	if got := Load().SMTPPort; got != 587 {
		t.Errorf("SMTPPort default = %d, want 587", got)
	}
}

// TRUSTED_PROXY_HOPS defaults to 0 (trust no forwarding header); a typo falls
// back to 0 too, and a negative count stops the server.
func TestLoad_TrustedProxyHops(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int
	}{{"", 0}, {"1", 1}, {"2", 2}, {"one", 0}} {
		t.Run(c.raw, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXY_HOPS", c.raw)
			if got := Load().TrustedProxyHops; got != c.want {
				t.Errorf("TRUSTED_PROXY_HOPS=%q: got %d, want %d", c.raw, got, c.want)
			}
		})
	}
	t.Setenv("ENV", "development")
	t.Setenv("TRUSTED_PROXY_HOPS", "-1")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_HOPS") {
		t.Errorf("Validate with -1 = %v, want a TRUSTED_PROXY_HOPS error", err)
	}
}

// Outside development the JWT secret must be a real one: not the built-in
// default, not the env.example placeholder, and at least 32 bytes.
func TestValidate_RejectsWeakJWTSecretOutsideDevelopment(t *testing.T) {
	for _, secret := range []string{"", "dev-secret-change-me", "CHANGE_ME", "change_me", "CHANGE_ME_0123456789abcdef0123456789abcdef", "short-secret"} {
		t.Run(secret, func(t *testing.T) {
			t.Setenv("ENV", "production")
			t.Setenv("AI_SECRETS_KEY", "a-real-key")
			t.Setenv("JWT_SECRET", secret)
			if err := Load().Validate(); err == nil {
				t.Errorf("Validate() = nil, want an error for JWT_SECRET %q", secret)
			}
		})
	}
}

func TestValidate_AcceptsStrongJWTSecretOutsideDevelopment(t *testing.T) {
	t.Setenv("ENV", "staging")
	t.Setenv("AI_SECRETS_KEY", "a-real-key")
	t.Setenv("JWT_SECRET", strongJWTSecret)
	if err := Load().Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidate_DevelopmentAllowsDefaultJWTSecret(t *testing.T) {
	t.Setenv("ENV", "")
	t.Setenv("AI_SECRETS_KEY", "")
	t.Setenv("JWT_SECRET", "")
	cfg := Load()
	if cfg.JWTSecret != DevJWTSecret {
		t.Fatalf("JWTSecret = %q, want the dev default", cfg.JWTSecret)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil in development", err)
	}
}

const strongJWTSecret = "0123456789abcdef0123456789abcdef" // gitleaks:allow
