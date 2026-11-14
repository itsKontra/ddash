package cloudflareapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// api calls Cloudflare's API v4 with the token as a Bearer token. Every
// answer is the {success, errors, messages, result, result_info}
// envelope.
type api struct {
	base, token string
	http        *http.Client
}

// envelope is Cloudflare's answer around every result.
type envelope struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends body (nil for none) as JSON and decodes the result into out
// (nil to drop it). A failure is a gRPC error: PERMISSION_DENIED when
// Cloudflare refuses the token, NOT_FOUND, FAILED_PRECONDITION for a
// write it refuses, else UNAVAILABLE.
func (a *api) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	_, err := a.do(ctx, method, path, query, body, out)
	return err
}

// do is call, also returning how many pages the list answered has.
func (a *api) do(ctx context.Context, method, path string, query url.Values, body, out any) (pages int, err error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, status.Error(codes.Internal, err.Error())
		}
		r = bytes.NewReader(b)
	}
	target := a.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return 0, status.Error(codes.Internal, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return 0, status.Errorf(codes.Unavailable, "Cloudflare unreachable: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return 0, status.Errorf(codes.Unavailable, "Cloudflare's answer broke off: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode/100 != 2 {
			return 0, refused(resp.StatusCode, nil)
		}
		return 0, status.Errorf(codes.Unavailable, "Cloudflare answered %d with no JSON", resp.StatusCode)
	}
	if resp.StatusCode/100 != 2 || !env.Success {
		return 0, refused(resp.StatusCode, env.Errors)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return 0, status.Errorf(codes.Unavailable, "Cloudflare's answer to %s %s: %v", method, path, err)
		}
	}
	if env.ResultInfo != nil {
		pages = env.ResultInfo.TotalPages
	}
	return pages, nil
}

// list GETs every page of a list, perPage at a time, decoding each into a
// slice and appending it to out.
func list[T any](ctx context.Context, a *api, path string, query url.Values, perPage int) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(perPage))
		var batch []T
		pages, err := a.do(ctx, http.MethodGet, path, q, nil, &batch)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if page >= pages || len(batch) == 0 {
			return all, nil
		}
	}
}

// refused is Cloudflare's refusal as a gRPC error, its errors in the
// message.
func refused(code int, errs []apiError) error {
	var whys []string
	for _, e := range errs {
		whys = append(whys, fmt.Sprintf("%s (%d)", strings.TrimSuffix(e.Message, "."), e.Code))
	}
	why := strings.Join(whys, "; ")
	if why == "" {
		why = strings.ToLower(http.StatusText(code))
	}
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return status.Errorf(codes.PermissionDenied, "Cloudflare refused the API token: %s", why)
	case code == http.StatusNotFound:
		return status.Errorf(codes.NotFound, "Cloudflare answered 404: %s", why)
	case code == http.StatusTooManyRequests || code >= 500:
		return status.Errorf(codes.Unavailable, "Cloudflare answered %d: %s", code, why)
	}
	return status.Errorf(codes.FailedPrecondition, "Cloudflare refused it: %s", why)
}
