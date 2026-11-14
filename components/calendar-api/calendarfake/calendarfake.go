// Package calendarfake serves iCalendar files whose dates follow the
// clock, so a calendar faked from fixtures always has events today, just
// past and coming up. Each <name>.ics in a scenario directory (falling
// back to its sibling baseline) is a text/template answering GET
// /<name>.ics, rendered at request time with:
//
//	{{date N}}         the local date N days from now, e.g. 20261116
//	{{at N "18:30"}}   that local time on day N, in UTC, e.g. 20261116T173000Z
//	{{local N "18:30"}} the same as a floating local time, e.g. 20261116T183000,
//	                   for a DTSTART;TZID=…
//	{{stamp}}          now in UTC, for DTSTAMP
//
// "Local" is the zone of Options.Now's times. Any other request gets 404.
// calendar-api's tests and fakelab serve it.
package calendarfake

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// Options shape a Fake.
type Options struct {
	// Now is the clock the dates follow; time.Now when nil.
	Now func() time.Time
	// Miss is told about a request no file answers.
	Miss func(request string, err error)
}

// Fake is the iCalendar server, an http.Handler.
type Fake struct {
	opts      Options
	calendars map[string]*template.Template
}

// New serves the .ics templates in dir and in its sibling baseline.
func New(dir string, opts Options) (*Fake, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Miss == nil {
		opts.Miss = func(string, error) {}
	}
	f := &Fake{opts: opts, calendars: map[string]*template.Template{}}
	dirs := []string{filepath.Join(filepath.Dir(dir), "baseline"), dir}
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.ics"))
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			name := filepath.Base(file)
			t, err := template.New(name).Funcs(funcs(time.Time{})).Parse(string(raw))
			if err != nil {
				return nil, fmt.Errorf("calendarfake: %w", err)
			}
			f.calendars["/"+name] = t
		}
	}
	return f, nil
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request := r.Method + " " + r.URL.Path
	t, ok := f.calendars[r.URL.Path]
	if !ok || r.Method != http.MethodGet {
		f.opts.Miss(request, fmt.Errorf("no calendar %s", r.URL.Path))
		http.NotFound(w, r)
		return
	}
	var body bytes.Buffer
	if err := template.Must(t.Clone()).Funcs(funcs(f.opts.Now())).Execute(&body, nil); err != nil {
		f.opts.Miss(request, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	// iCalendar lines end in CRLF.
	_, _ = w.Write(bytes.ReplaceAll(bytes.ReplaceAll(body.Bytes(), []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n")))
}

const (
	dateFormat  = "20060102"
	localFormat = "20060102T150405"
)

func funcs(now time.Time) template.FuncMap {
	day := func(n int, clock string) time.Time {
		c, err := time.Parse("15:04", clock)
		if err != nil {
			panic(fmt.Sprintf("calendarfake: %q is no HH:MM", clock)) // text/template reports it as the template's error
		}
		d := now.AddDate(0, 0, n)
		return time.Date(d.Year(), d.Month(), d.Day(), c.Hour(), c.Minute(), 0, 0, now.Location())
	}
	return template.FuncMap{
		"date":  func(n int) string { return now.AddDate(0, 0, n).Format(dateFormat) },
		"at":    func(n int, clock string) string { return day(n, clock).UTC().Format(localFormat) + "Z" },
		"local": func(n int, clock string) string { return day(n, clock).Format(localFormat) },
		"stamp": func() string { return now.UTC().Format(localFormat) + "Z" },
	}
}
