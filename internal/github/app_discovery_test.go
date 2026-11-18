package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/eyupio/zoomies/internal/store"
)

// GitHub's status-filtered run index can omit a queued run that its ordinary
// listing and direct job endpoint already expose. Polling must find that work
// without relying on another push or webhook to wake the fleet.
func TestThePollerFindsRecentJobsMissingFromTheStatusIndex(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale_index_%t", stale), func(t *testing.T) {
			var mu sync.Mutex
			reads := map[string]int{}
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				reads[r.URL.RequestURI()]++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/widgets/actions/runs":
					runs := `[]`
					switch r.URL.Query().Get("status") {
					case "":
						runs = `[{"id":1,"status":"queued"},{"id":2,"status":"in_progress"},{"id":3,"status":"completed"}]`
					case "queued":
						if stale {
							// Run 1 is missing; run 4 is old enough to be off the recent page.
							runs = `[{"id":4,"status":"queued"},{"id":2,"status":"queued"}]`
						}
					case "in_progress":
						if stale {
							runs = `[{"id":2,"status":"in_progress"}]`
						}
					}
					_, _ = fmt.Fprintf(w, `{"workflow_runs":%s}`, runs)
				case "/repos/acme/widgets/actions/runs/1/jobs":
					if r.URL.Query().Get("page") == "2" {
						_, _ = w.Write([]byte(`{"jobs":[{"id":11,"status":"queued"},{"id":12,"status":"completed"}]}`))
					} else {
						w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2&per_page=100>; rel="next"`, srv.URL, r.URL.Path))
						_, _ = w.Write([]byte(`{"jobs":[{"id":10,"status":"queued"}]}`))
					}
				case "/repos/acme/widgets/actions/runs/2/jobs":
					_, _ = w.Write([]byte(`{"jobs":[{"id":20,"status":"in_progress"}]}`))
				case "/repos/acme/widgets/actions/runs/4/jobs":
					_, _ = w.Write([]byte(`{"jobs":[{"id":40,"status":"queued"}]}`))
				default:
					t.Errorf("unexpected read %s", r.URL.RequestURI())
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			client, err := newGitHubClient(srv.Client(), srv.URL+"/", srv.URL+"/")
			if err != nil {
				t.Fatal(err)
			}
			c := newAppClient(client, client, "acme/widgets", store.TargetRepo, 1, "https://github.com")
			jobs, err := c.ListQueuedJobs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, job := range jobs {
				ids = append(ids, job.ID)
			}
			want := []int64{10, 11, 20}
			if stale {
				want = append(want, 40)
			}
			if !slices.Equal(ids, want) {
				t.Fatalf("jobs = %v, want %v: recent and older unfinished jobs, without completed jobs", ids, want)
			}
			mu.Lock()
			defer mu.Unlock()
			for endpoint, count := range reads {
				if count != 1 {
					t.Errorf("read %s %d times; overlapping run listings must not repeat job pages", endpoint, count)
				}
			}
		})
	}
}

func TestRecentRunDiscoveryKeepsTheSweepsRequestBudget(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/widgets/actions/runs":
			if r.URL.Query().Get("status") == "" {
				_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"queued"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
			}
		case "/repos/acme/widgets/actions/runs/1/jobs":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				page = 1
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d&per_page=100>; rel="next"`, srv.URL, r.URL.Path, page+1))
			_, _ = fmt.Fprintf(w, `{"jobs":[{"id":%d,"status":"queued"}]}`, page)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := newGitHubClient(srv.Client(), srv.URL+"/", srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	c := newAppClient(client, client, "acme/widgets", store.TargetRepo, 1, "https://github.com")
	jobs, err := c.ListQueuedJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != maxPollRequests-1 {
		t.Fatalf("found %d jobs, want the %d pages left after the recent-run listing", len(jobs), maxPollRequests-1)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != maxPollRequests {
		t.Fatalf("made %d requests, want the unchanged bound %d", requests, maxPollRequests)
	}
}
