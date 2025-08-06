package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
)

const DefaultPageSize = 50

// BuildURL combines the base API URL with a formatted endpoint path.
func buildURL(base, path string, params ...interface{}) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	formatted := fmt.Sprintf(path, params...)
	endpoint, err := url.Parse(formatted)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint path: %w", err)
	}
	return baseURL.ResolveReference(endpoint), nil
}

// DoRequestCommon executes the HTTP request and handles rate limit annotations.
func doRequestCommon(wrapper *uhttp.BaseHttpClient, req *http.Request, res interface{}) (http.Header, annotations.Annotations, error) {
	opts := []uhttp.DoOption{}
	if res != nil {
		opts = append(opts, uhttp.WithJSONResponse(res))
	}
	opts = append(opts, uhttp.WithErrorResponse(&ErrorResponse{}))
	resp, err := wrapper.Do(req, opts...)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	ann := annotations.Annotations{}
	if desc, err := ratelimit.ExtractRateLimitData(resp.StatusCode, &resp.Header); err == nil {
		ann.WithRateLimiting(desc)
	}
	return resp.Header, ann, nil
}

// EncodePageToken serializes pageToken to a base64 string.
func encodePageToken(pt *pageToken) string {
	b, _ := json.Marshal(pt)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodePageToken deserializes a base64 token string back into a pageToken struct.
func decodePageToken(token string) (*pageToken, error) {
	if token == "" {
		return &pageToken{StartPosition: 0}, nil
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	var pt pageToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, err
	}
	return &pt, nil
}

// PreparePagedRequest prepares the URL for a paged request.
func preparePagedRequest(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()
	if options.PageToken != "" {
		pt, err := decodePageToken(options.PageToken)
		if err != nil {
			return nil, fmt.Errorf("invalid page token: %w", err)
		}
		q.Set("start_position", fmt.Sprintf("%d", pt.StartPosition))
	} else {
		q.Set("start_position", "0")
	}

	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	q.Set("count", fmt.Sprintf("%d", pageSize))

	fullURL.RawQuery = q.Encode()
	return fullURL, nil
}

// GetNextToken calculates the token for the next page based on the response.
func getNextToken(responsePage Page) string {
	// responsePage.EndPosition will always be 1 position behind the actual end of the list.
	// So we must add 1 to validate if we are at the last value.
	if responsePage.EndPosition+1 < responsePage.TotalSetSize {
		return encodePageToken(&pageToken{
			StartPosition: responsePage.EndPosition + 1,
		})
	}
	return ""
}

func ApplyQueryParam(reqURL *url.URL, key string, value string) {
	q := reqURL.Query()
	q.Set(key, value)
	reqURL.RawQuery = q.Encode()
}

// CacheItem holds cached User Info data with expiry time.
type CacheItem struct {
	UserInfo  *UserInfoResponse
	ExpiresAt time.Time
}

// FetchFunc is a function type for fetching User Info data.
type FetchFunc func() (*UserInfoResponse, time.Duration, error)

// UserInfoCache provides thread-safe caching for User Info responses.
type UserInfoCache struct {
	item  *CacheItem
	mutex sync.RWMutex
}

// NewUserInfoCache creates a new UserInfoCache instance.
func NewUserInfoCache() *UserInfoCache {
	return &UserInfoCache{}
}

// GetOrFetch retrieves User Info data from cache or fetches it if not present/expired.
func (c *UserInfoCache) GetOrFetch(fetchFunc FetchFunc) (*UserInfoResponse, error) {
	// First, try to get from cache with read lock
	c.mutex.RLock()
	if c.item != nil && time.Now().Before(c.item.ExpiresAt) {
		// Cache hit and not expired
		userInfo := c.item.UserInfo
		c.mutex.RUnlock()
		return userInfo, nil
	}
	c.mutex.RUnlock()

	// Cache miss or expired - need to fetch with write lock
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Double-check in case another goroutine fetched while we waited for write lock
	if c.item != nil && time.Now().Before(c.item.ExpiresAt) {
		return c.item.UserInfo, nil
	}

	// Fetch new data
	userInfo, ttl, err := fetchFunc()
	if err != nil {
		// Clear expired item on fetch failure
		c.item = nil
		return nil, err
	}

	// Store in cache
	c.item = &CacheItem{
		UserInfo:  userInfo,
		ExpiresAt: time.Now().Add(ttl),
	}

	return userInfo, nil
}
