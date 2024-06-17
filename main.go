package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/config"
	"lyrics-api-go/middleware"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
)

var conf = config.Get()

var (
	LyricsURL          = conf.Configuration.LyricsUrl
	TrackURL           = conf.Configuration.TrackUrl
	TokenURL           = conf.Configuration.TokenUrl
	TokenKey           = conf.Configuration.TokenKey
	AppPlatform        = conf.Configuration.AppPlatform
	UserAgent          = conf.Configuration.UserAgent
	CookieStringFormat = conf.Configuration.CookieStringFormat
	CookieValue        = conf.Configuration.CookieValue
)

var (
	cache      sync.Map
	httpClient *http.Client
)

type TokenData struct {
	AccessToken                      string `json:"accessToken"`
	AccessTokenExpirationTimestampMs int64  `json:"accessTokenExpirationTimestampMs"`
}

type Line struct {
	StartTimeMs string   `json:"startTimeMs"`
	Words       string   `json:"words"`
	Syllables   []string `json:"syllables"`
	EndTimeMs   string   `json:"endTimeMs"`
}

type LyricsResponse struct {
	Lyrics struct {
		SyncType string `json:"syncType"`
		Lines    []Line `json:"lines"`
	} `json:"lyrics"`
}

type TrackResponse struct {
	Tracks struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	} `json:"tracks"`
}

type CacheEntry struct {
	Value      string
	Expiration int64
}

type CacheDump map[string]CacheEntry

type CacheDumpResponse struct {
	NumberOfKeys int
	SizeInKB     int
	Cache        CacheDump
}

func init() {

	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)

	err := godotenv.Load()
	if err != nil {
		log.Warn("Error loading .env file, using environment variables")
	}

	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}
}

func main() {
	// start goroutine to invalidate cache
	go invalidateCache()

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/cache", getCacheDump)
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"help": "Use /getLyrics to get the lyrics of a song. Provide the song name and artist name as query parameters. Example: /getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran",
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"https://music.youtube.com", "http://localhost:3000"},
		AllowCredentials: true,
	})

	limiter := middleware.NewIPRateLimiter(rate.Limit(conf.Configuration.RateLimitPerSecond), conf.Configuration.RateLimitBurstLimit)

	// logging middleware

	loggedRouter := middleware.LoggingMiddleware(router)
	// chain cors middleware
	corsHandler := c.Handler(loggedRouter)

	//chain rate limiter
	handler := limitMiddleware(corsHandler, limiter)

	log.Infof("Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))

}

func setCommonHeaders(req *http.Request) {
	req.Header.Set("App-Platform", AppPlatform)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("cookie", fmt.Sprintf(CookieStringFormat, CookieValue))
}

func makeHTTPRequest(method, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}

	setCommonHeaders(req)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status code %d", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func getCache(key string) (string, bool) {
	entry, ok := cache.Load(key)
	if !ok {
		return "", false
	}
	cacheEntry := entry.(CacheEntry)
	if time.Now().UnixNano() > cacheEntry.Expiration {
		cache.Delete(key)
		return "", false
	}
	return cacheEntry.Value, true
}

func setCache(key, value string, duration time.Duration) {
	cacheEntry := CacheEntry{
		Value:      value,
		Expiration: time.Now().Add(duration).UnixNano(),
	}
	cache.Store(key, cacheEntry)
}

func getValidAccessToken() (string, error) {
	if token, ok := getCache(TokenKey); ok {
		log.Info("[Cache:Token] Using cached token")
		return token, nil
	}

	body, err := makeHTTPRequest("GET", TokenURL, nil)
	if err != nil {
		return "", err
	}

	var tokenData TokenData
	if err := json.Unmarshal(body, &tokenData); err != nil {
		return "", err
	}

	expiresInSeconds := int64((tokenData.AccessTokenExpirationTimestampMs - time.Now().UnixNano()/int64(time.Millisecond)) / 1000)
	setCache(TokenKey, tokenData.AccessToken, time.Duration(expiresInSeconds)*time.Second)

	return tokenData.AccessToken, nil
}

func getLyrics(w http.ResponseWriter, r *http.Request) {
	songName := r.URL.Query().Get("s") + r.URL.Query().Get("song") + r.URL.Query().Get("songName")
	artistName := r.URL.Query().Get("a") + r.URL.Query().Get("artist") + r.URL.Query().Get("artistName")
	customTrackID := r.URL.Query().Get("t_id") + r.URL.Query().Get("trackId")

	if (songName == "" && artistName == "") && customTrackID == "" {
		http.Error(w, "Song name or artist name not provided", http.StatusUnprocessableEntity)
		return
	}

	accessToken, err := getValidAccessToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var trackID string
	if customTrackID != "" {
		trackID = customTrackID
	} else {
		query := url.QueryEscape(songName + " " + artistName)
		cacheKey := fmt.Sprintf("track:%s", query)
		if cachedTrackID, ok := getCache(cacheKey); ok {
			log.Infof("[Cache:Track] Found cached track id: %s", cachedTrackID)
			trackID = cachedTrackID
		} else {
			trackID, err = fetchTrackID(query, accessToken)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if trackID != "" {
				log.Warnf("[Cache:Track] Caching track id: %s", trackID)
				setCache(cacheKey, trackID, time.Duration(conf.Configuration.TrackCacheTTLInSeconds)*time.Second)
			} else {
				http.Error(w, "Track not found", http.StatusNotFound)
				return
			}
		}
	}

	lyricsURL := LyricsURL + trackID + "?format=json&market=from_token"
	cacheKey := fmt.Sprintf("lyrics:%s", trackID)
	if cachedLyrics, ok := getCache(cacheKey); ok {
		log.Info("[Cache:Lyrics] Found cached lyrics")
		w.Header().Set("Content-Type", "application/json")
		lyrics := []Line{}
		json.Unmarshal([]byte(cachedLyrics), &lyrics)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   nil,
			"trackId": trackID,
			"lyrics":  lyrics,
		})
		return
	}

	lyrics, err := fetchLyrics(lyricsURL, accessToken)
	if err != nil {
		fmt.Println("Error fetching lyrics: ", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if lyrics == nil {
		http.Error(w, "Lyrics not available for this track", http.StatusNotFound)
		return
	}

	log.Warn("[Cache:Lyrics] Caching lyrics")
	cacheValue, _ := json.Marshal(lyrics)
	setCache(cacheKey, string(cacheValue), time.Duration(conf.Configuration.LyricsCacheTTLInSeconds)*time.Second)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":   nil,
		"trackId": trackID,
		"lyrics":  lyrics,
	})
}

func fetchTrackID(query, accessToken string) (string, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	body, err := makeHTTPRequest("GET", TrackURL+query, headers)
	if err != nil {
		return "", err
	}

	var trackResp TrackResponse
	if err := json.Unmarshal(body, &trackResp); err != nil {
		return "", err
	}

	if len(trackResp.Tracks.Items) > 0 {
		return trackResp.Tracks.Items[0].ID, nil
	}

	return "", nil
}

func fetchLyrics(lyricsURL, accessToken string) ([]Line, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	body, err := makeHTTPRequest("GET", lyricsURL, headers)
	if err != nil {
		return nil, err
	}

	if body == nil {
		return nil, nil
	}

	var lyricsResp LyricsResponse
	if err := json.Unmarshal(body, &lyricsResp); err != nil {
		return nil, err
	}

	return lyricsResp.Lyrics.Lines, nil
}

func getCacheDump(w http.ResponseWriter, r *http.Request) {
	// Check if the request is authorized by checking the access token
	if r.Header.Get("Authorization") != conf.Configuration.CacheAccessToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	cacheDump := CacheDump{}
	cacheDumpResponse := CacheDumpResponse{}
	cache.Range(func(key, value interface{}) bool {
		if key == "accessToken" {
			return true
		}
		cacheDump[key.(string)] = value.(CacheEntry)
		return true
	})
	cacheDumpResponse.Cache = cacheDump
	cacheDumpResponse.NumberOfKeys = len(cacheDump)
	size := 0
	for key, value := range cacheDump {
		size += len(key) + len(value.Value) + 8
	}
	cacheDumpResponse.SizeInKB = size / 1024

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cacheDumpResponse)
}

func limitMiddleware(next http.Handler, limiter *middleware.IPRateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limiter := limiter.GetLimiter(r.RemoteAddr)
		if !limiter.Allow() {
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// goroutine to invalidate cache every 1 hour based on expiration times and delete keys
func invalidateCache() {
	log.Infof("[Cache:Invalidation] Starting cache invalidation goroutine")
	for {
		time.Sleep(time.Duration(conf.Configuration.CacheInvalidationIntervalInSeconds) * time.Second)
		cache.Range(func(key, value interface{}) bool {
			cacheEntry := value.(CacheEntry)
			if time.Now().UnixNano() > cacheEntry.Expiration {
				cache.Delete(key)
				fmt.Printf("\033[31m[Cache:Invalidation] Deleted key: %s\033[0m\n", key)
			}
			return true
		})
	}
}
