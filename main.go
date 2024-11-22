package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"lyrics-api-go/config"
	"lyrics-api-go/middleware"
	"lyrics-api-go/utils"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	ClientID           = conf.Configuration.ClientID
	ClientSecret       = conf.Configuration.ClientSecret
	OauthTokenUrl      = conf.Configuration.OauthTokenUrl
	OauthTokenKey      = conf.Configuration.OauthTokenKey
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
	DurationMs  string   `json:"durationMs"`
	Words       string   `json:"words"`
	Syllables   []string `json:"syllables"`
	EndTimeMs   string   `json:"endTimeMs"`
}

type LyricsResponse struct {
	Lyrics struct {
		SyncType      string `json:"syncType"`
		Lines         []Line `json:"lines"`
		IsRtlLanguage bool   `json:"isRtlLanguage"`
		Language      string `json:"language"`
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

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
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

func isRTLLanguage(langCode string) bool {
	rtlLanguages := map[string]bool{
		"ar": true, // Arabic
		"fa": true, // Persian (Farsi)
		"he": true, // Hebrew
		"ur": true, // Urdu
		"ps": true, // Pashto
		"sd": true, // Sindhi
		"ug": true, // Uyghur
		"yi": true, // Yiddish
		"ku": true, // Kurdish (some dialects)
		"dv": true, // Divehi (Maldivian)
	}
	return rtlLanguages[langCode]
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
	if conf.FeatureFlags.CacheCompression {
		// Decompress the value before returning
		decompressedValue, err := utils.DecompressString(cacheEntry.Value)
		if err != nil {
			log.Errorf("Error decompressing cache value: %v", err)
			return "", false
		}
		return decompressedValue, true
	} else {
		return cacheEntry.Value, true
	}
}

func setCache(key, value string, duration time.Duration) {
	var cacheEntry CacheEntry

	if conf.FeatureFlags.CacheCompression {
		compressedValue, err := utils.CompressString(value)
		if err != nil {
			log.Errorf("Error compressing cache value: %v", err)
			return
		}
		cacheEntry = CacheEntry{
			Value:      compressedValue,
			Expiration: time.Now().Add(duration).UnixNano(),
		}
	} else {
		cacheEntry = CacheEntry{
			Value:      value,
			Expiration: time.Now().Add(duration).UnixNano(),
		}
	}

	cache.Store(key, cacheEntry)
}

func getOauthAccessToken(clientID, clientSecret string) (string, error) {

	if token, ok := getCache(OauthTokenKey); ok {
		log.Info("[Cache:OAuthToken] Using cached token")
		return token, nil
	}

	auth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))

	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequest("POST", OauthTokenUrl,
		strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("error creating token request: %v", err)
	}

	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making token request: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading token response: %v", err)
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("error parsing token response: %v", err)
	}

	log.Warn("[Cache:OAuthToken] Caching token")
	setCache(OauthTokenKey, tokenResp.AccessToken, time.Duration(tokenResp.ExpiresIn)*time.Second)

	return tokenResp.AccessToken, nil
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
			trackID, err = fetchTrackID(query, ClientID, ClientSecret)
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
		var cachedData map[string]interface{}
		json.Unmarshal([]byte(cachedLyrics), &cachedData)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         nil,
			"trackId":       trackID,
			"lyrics":        cachedData["lyrics"],
			"isRtlLanguage": cachedData["isRtlLanguage"],
			"language":      cachedData["language"],
		})
		return
	}

	lyrics, isRtlLanguage, language, err := fetchLyrics(lyricsURL, accessToken)
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
	cacheValue, _ := json.Marshal(map[string]interface{}{
		"lyrics":        lyrics,
		"isRtlLanguage": isRtlLanguage,
		"language":      language,
	})
	setCache(cacheKey, string(cacheValue), time.Duration(conf.Configuration.LyricsCacheTTLInSeconds)*time.Second)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":         nil,
		"trackId":       trackID,
		"lyrics":        lyrics,
		"isRtlLanguage": isRtlLanguage,
		"language":      language,
	})

}

func fetchTrackID(query, clientID, clientSecret string) (string, error) {
	accessToken, err := getOauthAccessToken(clientID, clientSecret)
	if err != nil {
		return "", fmt.Errorf("error getting access token: %v", err)
	}

	searchURL := TrackURL + query

	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}

	body, err := makeHTTPRequest("GET", searchURL, headers)
	if err != nil {
		return "", fmt.Errorf("error making search request: %v", err)
	}

	var trackResp TrackResponse
	if err := json.Unmarshal(body, &trackResp); err != nil {
		return "", fmt.Errorf("error parsing search response: %v", err)
	}

	if len(trackResp.Tracks.Items) > 0 {
		return trackResp.Tracks.Items[0].ID, nil
	}

	return "", nil
}

func fetchLyrics(lyricsURL, accessToken string) ([]Line, bool, string, error) {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	body, err := makeHTTPRequest("GET", lyricsURL, headers)
	if err != nil {
		return nil, false, "", err
	}

	if body == nil {
		return nil, false, "", nil
	}

	var lyricsResp LyricsResponse
	if err := json.Unmarshal(body, &lyricsResp); err != nil {
		return nil, false, "", err
	}

	lines := lyricsResp.Lyrics.Lines
	for i := 0; i < len(lines); i++ {
		startTime, _ := strconv.ParseInt(lines[i].StartTimeMs, 10, 64)
		var endTime int64

		if i == len(lines)-1 {
			endTime, _ = strconv.ParseInt(lines[i].StartTimeMs, 10, 64)
		} else {
			endTime, _ = strconv.ParseInt(lines[i+1].StartTimeMs, 10, 64)
		}

		duration := endTime - startTime
		lines[i].DurationMs = strconv.FormatInt(duration, 10)
	}
	language := lyricsResp.Lyrics.Language
	isRTL := isRTLLanguage(language)

	return lines, isRTL, language, nil
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
