package config

import (
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
)

var conf = mustLoad()

type Config struct {
	Configuration struct {
		RateLimitPerSecond                 int    `envconfig:"RATE_LIMIT_PER_SECOND" default:"2"`
		RateLimitBurstLimit                int    `envconfig:"RATE_LIMIT_BURST_LIMIT" default:"5"`
		CacheInvalidationIntervalInSeconds int    `envconfig:"CACHE_INVALIDATION_INTERVAL_IN_SECONDS" default:"3600"`
		LyricsCacheTTLInSeconds            int    `envconfig:"LYRICS_CACHE_TTL_IN_SECONDS" default:"86400"`
		TrackCacheTTLInSeconds             int    `envconfig:"TRACK_CACHE_TTL_IN_SECONDS" default:"3600"`
		CacheAccessToken                   string `envconfig:"CACHE_ACCESS_TOKEN" default:""`
		LyricsUrl                          string `envconfig:"LYRICS_URL" default:""`
		TrackUrl                           string `envconfig:"TRACK_URL" default:""`
		TokenUrl                           string `envconfig:"TOKEN_URL" default:""`
		TokenKey                           string `envconfig:"TOKEN_KEY"  default:""`
		AppPlatform                        string `envconfig:"APP_PLATFORM" default:""`
		UserAgent                          string `envconfig:"USER_AGENT" default:""`
		CookieStringFormat                 string `envconfig:"COOKIE_STRING_FORMAT" default:""`
		CookieValue                        string `envconfig:"COOKIE_VALUE" default:""`
	}

	FeatureFlags struct {
		CacheCompression bool `envconfig:"FF_CACHE_COMPRESSION" default:"true"`
	}
}

// load loads the configuration from the environment.
func load() (Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Warnf("Error loading env config: %v", err)
	}

	cfg := Config{}
	err = envconfig.Process("", &cfg)
	return cfg, err
}

func mustLoad() Config {
	c, err := load()
	if err != nil {
		log.WithError(err).Warnf("Unable to load configuration")
	}

	return c
}

func Get() Config {
	return conf
}
