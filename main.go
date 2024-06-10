package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

    log "github.com/sirupsen/logrus"
	"github.com/didip/tollbooth/v7"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

const (
	LyricsURL   = "https://spclient.wg.spotify.com/color-lyrics/v2/track/"
	TrackURL    = "https://api.spotify.com/v1/search?type=track&q="
	TokenURL    = "https://open.spotify.com/get_access_token?reason=transport&productType=web_player"
	TokenKey    = "accessToken"
	AppPlatform = "WebPlayer"
	UserAgent   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.0.0 Safari/537.36"
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

func setCommonHeaders(req *http.Request) {
	req.Header.Set("App-Platform", AppPlatform)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("cookie", fmt.Sprintf("sp_dc=%s", os.Getenv("SP_DC")))
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
				setCache(cacheKey, trackID, time.Hour)
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
	setCache(cacheKey, string(cacheValue), 24*time.Hour)

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

func main() {

    lmt := tollbooth.NewLimiter(2, nil)

	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"help": "Use /getLyrics to get the lyrics of a song. Provide the song name and artist name as query parameters. Example: /getLyrics?s=Shape%20of%20You&a=Ed%20Sheeran",
		})
	})

	loggedRouter := handlers.LoggingHandler(os.Stdout, router)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"https://music.youtube.com", "http://localhost:3000"},
		AllowCredentials: true,
	})

	handler := c.Handler(tollbooth.LimitHandler(lmt, loggedRouter))

	log.Infof("Server listening on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}
