package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

func TestGetLyrics(t *testing.T) {
	req, err := http.NewRequest("GET", "http://localhost:8080/getLyrics?s=Blue&a=Billie%20Eilish", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	router := mux.NewRouter()
	router.HandleFunc("/getLyrics", getLyrics)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotNil(t, response["lyrics"])

	expectedStatusCode := 200
	actualStatusCode := 500 // Replace with actual call to the function being tested
	if expectedStatusCode != actualStatusCode {
		t.Errorf("Expected status code %d, got %d", expectedStatusCode, actualStatusCode)
	}
}

func TestGetCacheDump(t *testing.T) {
	req, err := http.NewRequest("GET", "/cache", nil)
	assert.NoError(t, err)
	req.Header.Set("Authorization", conf.Configuration.CacheAccessToken)

	rr := httptest.NewRecorder()
	router := mux.NewRouter()
	router.HandleFunc("/cache", getCacheDump)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response CacheDumpResponse
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotNil(t, response.Cache)
}

func TestInvalidCacheDump(t *testing.T) {
	req, err := http.NewRequest("GET", "/cache", nil)
	assert.NoError(t, err)
	req.Header.Set("Authorization", "invalid_token")

	rr := httptest.NewRecorder()
	router := mux.NewRouter()
	router.HandleFunc("/cache", getCacheDump)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestFetchTrackID(t *testing.T) {
	trackID, err := fetchTrackID("Blue Billie Eilish", ClientID, ClientSecret)
	assert.NoError(t, err)
	assert.NotEmpty(t, trackID)

	url := "http://localhost:8080/search?query=Blue+Billie+Eilish" // Local URL for testing
	_, err = http.Get(url)
	if err != nil {
		t.Errorf("Error making search request: %v", err)
	}
}

func TestFetchLyrics(t *testing.T) {
	accessToken, err := getValidAccessToken()
	assert.NoError(t, err)

	trackID, err := fetchTrackID("Blue Billie Eilish", ClientID, ClientSecret)
	assert.NoError(t, err)
	assert.NotEmpty(t, trackID)

	lyricsURL := "http://localhost:8080/lyrics?track_id=" + trackID // Local URL for testing
	lines, isRtlLanguage, language, err := fetchLyrics(lyricsURL, accessToken)
	if err != nil {
		t.Fatalf("Failed to fetch lyrics: %v", err)
	}
	assert.NotNil(t, lines, "Expected lines to be not nil")
	assert.NotEmpty(t, language, "Expected language to be not empty")
	assert.IsType(t, false, isRtlLanguage, "Expected isRtlLanguage to be of type bool")
}

func TestCacheFunctions(t *testing.T) {
	key := "testKey"
	value := "testValue"
	duration := 1 * time.Second

	setCache(key, value, duration)
	cachedValue, ok := getCache(key)
	assert.True(t, ok)
	assert.Equal(t, value, cachedValue)

	time.Sleep(2 * time.Second)
	_, ok = getCache(key)
	assert.False(t, ok)
}
