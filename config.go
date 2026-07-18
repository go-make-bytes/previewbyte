package previewbyte

import (
	"strings"
	"time"

	azugocfg "azugo.io/azugo/config"
	corecfg "azugo.io/core/config"
	"azugo.io/core/validation"
	"github.com/spf13/viper"

	"github.com/gmb-lib/go-authbyte/authclient"
	pkconfig "github.com/gmb-lib/go-platform-kit/config"

	"github.com/go-make-bytes/previewbyte/render"
)

// Configuration is the preview/render service configuration: the platform base
// config, the inbound DPoP validation (this service's own audience), the document
// source it reads bytes from on behalf of the user, the outbound service-client
// identity used for the on-behalf token exchange, and the render caps that bound
// every parse of untrusted input.
//
// The service holds no durable data and no signing crypto: it streams bytes from
// the source, renders an inert preview, and returns it.
type Configuration struct {
	*pkconfig.BaseConfiguration `mapstructure:",squash"`

	// Auth is the inbound DPoP validation config (AUTH_ISSUER_URL / SERVICE_AUDIENCE
	// / ...). The only caller today is the backend-for-frontend, presenting a service
	// token for this service's audience and reading on behalf of the user.
	Auth *authclient.Configuration `mapstructure:"auth"`

	// DevAcceptUserToken is a development-only concession (mirrors the document and
	// signing services): when true the inbound auth validates the demo single-page
	// app's public-client DPoP user token (aud = DevUserAudience) and relaxes the
	// per-endpoint scope checks. MUST be false in production.
	DevAcceptUserToken bool   `mapstructure:"dev_accept_user_token"`
	DevUserAudience    string `mapstructure:"dev_user_token_audience"`

	// DocumentBaseURL is the document source this service reads bytes from;
	// DocumentAudience is the target audience for the delegated token. Empty
	// DocumentBaseURL means the by-reference preview is unavailable and fails closed.
	DocumentBaseURL  string `mapstructure:"document_base_url" validate:"omitempty,url"`
	DocumentAudience string `mapstructure:"document_audience"`

	// Outbound service-client identity. The client id/secret authenticate the
	// outbound DPoP service tokens used to read the source on behalf of the user;
	// OutboundIssuerURL points the token mint at the in-network auth address (the
	// issuer claim stays Auth.IssuerURL).
	ServiceClientID     string `mapstructure:"service_client_id"`
	ServiceClientSecret string `mapstructure:"service_client_secret"`
	OutboundIssuerURL   string `mapstructure:"outbound_issuer_url" validate:"omitempty,url"`

	// Render caps bound every parse of untrusted bytes: the output format + size,
	// the time/page ceilings that kill render-bombs, the input size cap, and the
	// content-type allowlist (the declared MIME is never trusted — it is sniffed).
	RenderMode        string        `mapstructure:"render_mode" validate:"required,oneof=raster"`
	RenderMaxDPI      int           `mapstructure:"render_max_dpi" validate:"required,gt=0,lte=600"`
	RenderMaxWidth    int           `mapstructure:"render_max_width" validate:"required,gt=0,lte=8192"`
	RenderImageFormat string        `mapstructure:"render_image_format" validate:"required,oneof=png"`
	RenderTimeout     time.Duration `mapstructure:"render_timeout" validate:"required,gt=0"`
	RenderMaxPages    int           `mapstructure:"render_max_pages" validate:"required,gt=0"`
	RenderPoolSize    int           `mapstructure:"render_pool_size" validate:"required,gt=0"`
	InputMaxBytes     int64         `mapstructure:"input_max_bytes" validate:"required,gt=0"`
	SupportedMime     string        `mapstructure:"supported_mime" validate:"required"`
}

// NewConfiguration returns the configuration skeleton for binding.
func NewConfiguration() *Configuration {
	return &Configuration{BaseConfiguration: pkconfig.New()}
}

// ServerCore returns the embedded azugo configuration.
func (c *Configuration) ServerCore() *azugocfg.Configuration {
	return c.Configuration
}

// Bind registers defaults and environment bindings.
func (c *Configuration) Bind(_ string, v *viper.Viper) {
	c.BaseConfiguration.Bind("", v)
	c.Auth = azugocfg.Bind(c.Auth, "auth", v)

	// Dev-only user-token concession (off by default).
	v.SetDefault("dev_user_token_audience", "portal-api")
	_ = v.BindEnv("dev_accept_user_token", "DEV_ACCEPT_USER_TOKEN")
	_ = v.BindEnv("dev_user_token_audience", "DEV_USER_TOKEN_AUDIENCE")

	// Document source (bytes read on behalf of the user).
	v.SetDefault("document_audience", "svc:document")
	_ = v.BindEnv("document_base_url", "DOCUMENT_BASE_URL")
	_ = v.BindEnv("document_audience", "DOCUMENT_AUDIENCE")

	// Outbound service-client identity.
	v.SetDefault("service_client_id", "svc:preview")
	loadSecret(v, "service_client_secret", "SERVICE_CLIENT_SECRET")
	_ = v.BindEnv("service_client_id", "SERVICE_CLIENT_ID")
	_ = v.BindEnv("service_client_secret", "SERVICE_CLIENT_SECRET")
	_ = v.BindEnv("outbound_issuer_url", "OUTBOUND_ISSUER_URL")

	// Render caps. Conservative defaults: a single rendered page is at most
	// max-width wide at the chosen DPI, the whole render is killed after the
	// timeout, only the first N pages are rendered, and inputs above the cap are
	// rejected before any parse. The MIME allowlist is the first slice's set
	// (PDF only); the declared type is sniffed, never trusted.
	v.SetDefault("render_mode", "raster")
	v.SetDefault("render_max_dpi", 150)
	v.SetDefault("render_max_width", 2048)
	v.SetDefault("render_image_format", "png")
	v.SetDefault("render_timeout", 20*time.Second)
	v.SetDefault("render_max_pages", 100)
	v.SetDefault("render_pool_size", 2)
	v.SetDefault("input_max_bytes", int64(64*1024*1024))
	v.SetDefault("supported_mime", "application/pdf")
	_ = v.BindEnv("render_mode", "RENDER_MODE")
	_ = v.BindEnv("render_max_dpi", "RENDER_MAX_DPI")
	_ = v.BindEnv("render_max_width", "RENDER_MAX_WIDTH")
	_ = v.BindEnv("render_image_format", "RENDER_IMAGE_FORMAT")
	_ = v.BindEnv("render_timeout", "RENDER_TIMEOUT")
	_ = v.BindEnv("render_max_pages", "RENDER_MAX_PAGES")
	_ = v.BindEnv("render_pool_size", "RENDER_POOL_SIZE")
	_ = v.BindEnv("input_max_bytes", "INPUT_MAX_BYTES")
	_ = v.BindEnv("supported_mime", "SUPPORTED_MIME")
}

// Validate validates the full configuration tree.
func (c *Configuration) Validate(valid *validation.Validate) error {
	if err := c.BaseConfiguration.Validate(valid); err != nil {
		return err
	}
	if err := c.Auth.Validate(valid); err != nil {
		return err
	}

	return valid.Struct(c)
}

// DocumentEnabled reports whether a document source is configured, so the source
// client is worth building. Without it the by-reference preview fails closed.
func (c *Configuration) DocumentEnabled() bool {
	return strings.TrimSpace(c.DocumentBaseURL) != ""
}

// OutboundEnabled reports whether any outbound on-behalf call is configured (today,
// the document source), so the outbound auth client is worth building.
func (c *Configuration) OutboundEnabled() bool {
	return c.DocumentEnabled()
}

// RenderConfig projects the render caps onto the render package's config.
func (c *Configuration) RenderConfig() render.Config {
	return render.Config{
		PoolSize:      c.RenderPoolSize,
		MaxDPI:        c.RenderMaxDPI,
		MaxWidth:      c.RenderMaxWidth,
		ImageFormat:   c.RenderImageFormat,
		Timeout:       c.RenderTimeout,
		MaxPages:      c.RenderMaxPages,
		InputMaxBytes: c.InputMaxBytes,
		SupportedMime: supportedMimeSet(c.SupportedMime),
	}
}

// supportedMimeSet splits the comma-separated allowlist into a set of lowercase
// media types.
func supportedMimeSet(s string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range strings.Split(s, ",") {
		if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
			out[m] = true
		}
	}

	return out
}

// outboundIssuer returns the issuer base for the outbound service-token mint.
func (c *Configuration) outboundIssuer() string {
	if u := strings.TrimSpace(c.OutboundIssuerURL); u != "" {
		return u
	}

	return c.Auth.IssuerURL
}

// OutboundAuthClientConfig builds the outbound auth-client config: it reuses the
// validated inbound Auth settings and adds this service's client credentials + the
// optional issuer override.
func (c *Configuration) OutboundAuthClientConfig() *authclient.Configuration {
	cfg := *c.Auth // copy the validated inbound config
	cfg.IssuerURL = c.outboundIssuer()
	cfg.ServiceClientID = c.ServiceClientID
	cfg.ServiceClientSecret = c.ServiceClientSecret

	return &cfg
}

// loadSecret resolves a secret from the secret store (Vault agent -> <NAME>_FILE)
// and registers it as a default so an explicit env value still overrides it.
func loadSecret(v *viper.Viper, key, name string) {
	if secret, err := corecfg.LoadRemoteSecret(name); err == nil && secret != "" {
		v.SetDefault(key, secret)
	}
}
