package webmania

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	docs "github.com/webmaniabr/webmania-go/model/api2monitordocuments"
	reads "github.com/webmaniabr/webmania-go/model/api2monitorreads"
	"github.com/webmaniabr/webmania-go/value"
)

// PaginationOptions bounds work. The explicit query page and per_page remain unchanged.
type PaginationOptions struct {
	MaxPages, MaxItems int64
	Timeout            time.Duration
}
type Continuation struct {
	Page  int64
	Index int
}

// Iterator is sequential. Next checks context and its deadline even while yielding buffered items.
type Iterator[T any] struct {
	base                     *baseClient
	oid                      string
	path, query              map[string]any
	page, size, pages, items int64
	buffer                   []T
	index                    int
	last                     bool
	end                      string
	opts                     PaginationOptions
	deadline                 time.Time
}

func newIterator[T any](b *baseClient, oid string, path map[string]any, q any, o PaginationOptions) (*Iterator[T], error) {
	if o.MaxPages == 0 {
		o.MaxPages = 100
	}
	if o.MaxItems == 0 {
		o.MaxItems = 10000
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.MaxPages < 1 || o.MaxItems < 1 || o.Timeout < 1 {
		return nil, newError("validation")
	}
	_, s, e := snapshot(q, b.t.requestLimit)
	if e != nil {
		return nil, e
	}
	m := object(s)
	if e = schemaCheck(operations[oid].Module, operations[oid].Query, m); e != nil {
		return nil, e
	}
	page, size := int64(1), int64(50)
	if v, ok := m["page"]; ok {
		n, ok := v.(json.Number)
		if !ok {
			return nil, newError("validation")
		}
		page, e = n.Int64()
		if e != nil || page > math.MaxInt64-1 {
			return nil, newError("pagination_limit")
		}
	}
	if v, ok := m["per_page"]; ok {
		size, e = v.(json.Number).Int64()
		if e != nil {
			return nil, newError("validation")
		}
	}
	return &Iterator[T]{base: b, oid: oid, path: path, query: m, page: page, size: size, opts: o, deadline: time.Now().Add(o.Timeout)}, nil
}
func (i *Iterator[T]) Continuation() Continuation {
	if i.pages > 0 && i.index == len(i.buffer) && !i.last && i.page < math.MaxInt64-1 {
		return Continuation{i.page + 1, 0}
	}
	return Continuation{i.page, i.index}
}
func (i *Iterator[T]) StopReason() string { return i.end }
func (i *Iterator[T]) Next(ctx context.Context) (T, bool, error) {
	var zero T
	if e := ctx.Err(); e != nil {
		return zero, false, canceledError(e)
	}
	if time.Now().After(i.deadline) {
		return zero, false, newError("pagination_timeout")
	}
	if i.end != "" {
		return zero, false, nil
	}
	if i.items >= i.opts.MaxItems {
		i.end = "item_limit"
		return zero, false, nil
	}
	if i.index == len(i.buffer) {
		if i.last {
			i.end = "complete"
			return zero, false, nil
		}
		if i.pages >= i.opts.MaxPages {
			i.end = "page_limit"
			return zero, false, nil
		}
		targetPage := i.page
		if i.pages > 0 {
			if i.page == math.MaxInt64-1 {
				return zero, false, newError("pagination_limit")
			}
			targetPage++
		}
		i.query["page"] = json.Number(intText(targetPage))
		i.query["per_page"] = json.Number(intText(i.size))
		callCtx, cancel := context.WithDeadline(ctx, i.deadline)
		r, e := i.base.execute(callCtx, i.oid, i.path, i.query, nil, FileAuth{})
		cancel()
		if e != nil {
			return zero, false, e
		}
		var p struct {
			Data []T `json:"data"`
			Meta struct {
				CurrentPage int64 `json:"current_page"`
				LastPage    int64 `json:"last_page"`
				PerPage     int64 `json:"per_page"`
				Total       int64 `json:"total"`
			} `json:"meta"`
		}
		if e = value.UnmarshalExact(r.raw, &p); e != nil {
			return zero, false, newError("pagination_protocol")
		}
		expectedLast := p.Meta.Total / i.size
		if p.Meta.Total%i.size != 0 {
			expectedLast++
		}
		if expectedLast < 1 {
			expectedLast = 1
		}
		if p.Meta.LastPage != expectedLast {
			return zero, false, newError("pagination_protocol")
		}
		if p.Meta.CurrentPage != targetPage || p.Meta.PerPage != i.size || int64(len(p.Data)) > i.size {
			return zero, false, newError("pagination_protocol")
		}
		if len(p.Data) == 0 && targetPage < p.Meta.LastPage {
			return zero, false, newError("pagination_protocol")
		}
		if targetPage == p.Meta.LastPage {
			expectedCount := p.Meta.Total % i.size
			if expectedCount == 0 && p.Meta.Total > 0 {
				expectedCount = i.size
			}
			if int64(len(p.Data)) != expectedCount {
				return zero, false, newError("pagination_protocol")
			}
		}
		if p.Meta.LastPage < targetPage && len(p.Data) > 0 {
			return zero, false, newError("pagination_protocol")
		}
		if p.Meta.LastPage > targetPage && int64(len(p.Data)) != i.size {
			return zero, false, newError("pagination_protocol")
		}
		i.page = targetPage
		i.buffer = p.Data
		i.index = 0
		i.pages++
		i.last = i.page >= p.Meta.LastPage
		if len(p.Data) == 0 {
			i.end = "complete"
			return zero, false, nil
		}
	}
	if e := ctx.Err(); e != nil {
		return zero, false, canceledError(e)
	}
	if time.Now().After(i.deadline) {
		return zero, false, newError("pagination_timeout")
	}
	v := i.buffer[i.index]
	i.index++
	i.items++
	return v, true, nil
}
func intText(v int64) string { b, _ := json.Marshal(v); return string(b) }
func (c *MonitorClient) Companies(p MonitorListCompaniesParams, o PaginationOptions) (*Iterator[reads.MonitorCompany], error) {
	return newIterator[reads.MonitorCompany](c.base, "monitorListCompanies", nil, p.Query, o)
}
func (c *MonitorClient) CompanyMunicipalities(p MonitorListCompanyMunicipalitiesParams, o PaginationOptions) (*Iterator[reads.MonitorMunicipalityRegistration], error) {
	return newIterator[reads.MonitorMunicipalityRegistration](c.base, "monitorListCompanyMunicipalities", map[string]any{"company": p.Company}, p.Query, o)
}
func (c *MonitorClient) Reports(p MonitorListReportsParams, o PaginationOptions) (*Iterator[docs.MonitorReport], error) {
	return newIterator[docs.MonitorReport](c.base, "monitorListReports", nil, p.Query, o)
}

// WaitOptions applies only to the documented report polling request and import status read.
// No issuance, cancellation, import submission or other write is retried.
type WaitOptions struct {
	MaxAttempts       int
	Interval, Timeout time.Duration
}

func checkedWait(o WaitOptions) (WaitOptions, error) {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 20
	}
	if o.Interval == 0 {
		o.Interval = 5 * time.Second
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.MaxAttempts < 1 || o.Interval < 5*time.Second || o.Timeout <= 0 {
		return o, newError("validation")
	}
	return o, nil
}
func wait(ctx context.Context, b *baseClient, oid string, path map[string]any, q, body any, o WaitOptions, report bool) (executionResult, error) {
	o, e := checkedWait(o)
	if e != nil {
		return executionResult{}, e
	}
	raw, _, e := snapshot(body, b.t.requestLimit)
	if e != nil {
		return executionResult{}, e
	}
	var frozen any
	if body != nil {
		frozen = json.RawMessage(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	var known, uuids []string
	for n := 0; n < o.MaxAttempts; n++ {
		r, e := b.executeKnown(ctx, oid, path, q, frozen, FileAuth{}, known, uuids)
		if e != nil {
			return r, e
		}
		v, _ := value.Decode(r.raw, 64)
		if report {
			walk(v, func(k string, v any) {
				if s, ok := v.(string); ok {
					if isUUIDKey(k) && isUUID(s) {
						uuids = append(uuids, s)
					}
					if sensitive.MatchString(k) {
						known = append(known, s)
						if strings.Contains(s, "://") {
							a, u := observeLocation(s)
							known = append(known, a...)
							uuids = append(uuids, u...)
						}
						if isUUID(s) {
							uuids = append(uuids, s)
						}
					}
				}
			})
		}
		state, _ := object(v)["status"].(string)
		if state == "concluido" || !report && state == "invalido" {
			return r, nil
		}
		if n == o.MaxAttempts-1 {
			break
		}
		timer := time.NewTimer(o.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return executionResult{}, canceledError(ctx.Err())
		case <-timer.C:
		}
	}
	return executionResult{}, newError("polling_limit")
}
func (c *NfeClient) WaitReport(ctx context.Context, p FiscalCreateOrPollReportParams, o WaitOptions) (*Result[fiscal.FiscalCreateOrPollReportResponse], error) {
	r, e := wait(ctx, c.base, "fiscalCreateOrPollReport", nil, p.Query, p.Body, o, true)
	if e != nil {
		return nil, e
	}
	var v fiscal.FiscalCreateOrPollReportResponse
	if e = json.Unmarshal(r.raw, &v); e != nil {
		return nil, r.decodeError()
	}
	return &Result[fiscal.FiscalCreateOrPollReportResponse]{Value: v, Status: r.status}, nil
}
func (c *MonitorClient) WaitImport(ctx context.Context, p MonitorGetImportStatusParams, o WaitOptions) (*Result[docs.MonitorGetImportStatusResponse], error) {
	r, e := wait(ctx, c.base, "monitorGetImportStatus", map[string]any{"uuid": p.Uuid}, p.Query, nil, o, false)
	if e != nil {
		return nil, e
	}
	var v docs.MonitorGetImportStatusResponse
	if e = json.Unmarshal(r.raw, &v); e != nil {
		return nil, r.decodeError()
	}
	return &Result[docs.MonitorGetImportStatusResponse]{Value: v, Status: r.status}, nil
}
