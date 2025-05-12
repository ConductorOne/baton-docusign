package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/ratelimit"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
)

const DefaultPageSize = 50

// BuildURL combines the base API URL with a formatted endpoint path.
func BuildURL(base, path string, params ...interface{}) (*url.URL, error) {
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
func DoRequestCommon(wrapper *uhttp.BaseHttpClient, req *http.Request, res interface{}) (http.Header, annotations.Annotations, error) {
	opts := []uhttp.DoOption{}
	if res != nil {
		opts = append(opts, uhttp.WithJSONResponse(res))
	}
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
func EncodePageToken(pt *pageToken) string {
	b, _ := json.Marshal(pt)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodePageToken deserializes a base64 token string back into a pageToken struct.
func DecodePageToken(token string) (*pageToken, error) {
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
func PreparePagedRequest(baseURL *url.URL, endpoint string, options PageOptions) (*url.URL, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	fullURL := baseURL.ResolveReference(endpointURL)
	q := fullURL.Query()
	if options.PageToken != "" {
		pt, err := DecodePageToken(options.PageToken)
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
func GetNextToken(responsePage Page) string {
	if responsePage.EndPosition < responsePage.TotalSetSize {
		return EncodePageToken(&pageToken{
			StartPosition: responsePage.EndPosition + 1,
		})
	}
	return ""
}
