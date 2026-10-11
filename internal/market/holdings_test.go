package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const nport = `<?xml version="1.0"?><edgarSubmission xmlns="http://www.sec.gov/edgar/nport"><formData>
<genInfo><seriesId>S1</seriesId><repPdDate>2026-06-30</repPdDate></genInfo><invstOrSecs>
<invstOrSec><name>Small Co</name><pctVal>0.05</pctVal><payoffProfile>Long</payoffProfile></invstOrSec>
<invstOrSec><name>APPLE INC</name><pctVal>6.5</pctVal><payoffProfile>Long</payoffProfile></invstOrSec>
<invstOrSec><name>N/A</name><title>Microsoft Corp</title><pctVal>5.25</pctVal><payoffProfile>Long</payoffProfile></invstOrSec>
<invstOrSec><name>Shorted</name><pctVal>3</pctVal><payoffProfile>Short</payoffProfile></invstOrSec>
</invstOrSecs></formData></edgarSubmission>`

func TestParseNPort(t *testing.T) {
	h, err := parseNPort(strings.NewReader(nport), "S1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Top) != 2 || h.Top[0].Name != "Apple Inc." || h.Top[0].Weight != 650 || h.Top[1].Name != "Microsoft Corp." || h.Top[1].Weight != 525 {
		t.Errorf("top = %+v", h.Top)
	}
	if h.Count != 3 || h.AsOf.Format("2006-01-02") != "2026-06-30" {
		t.Errorf("count %d, as of %v", h.Count, h.AsOf)
	}
	// A trust files one report per fund; a different series' report is not this fund's.
	if _, err := parseNPort(strings.NewReader(nport), "S2", 2); !errors.Is(err, ErrNoHoldings) {
		t.Errorf("wrong series: err = %v", err)
	}
}

func TestSECHoldings(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/company_tickers_mf.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"fields":["cik","seriesId","classId","symbol"],"data":[[36405,"S1","C1","VOO"]]}`))
	})
	mux.HandleFunc("/cgi-bin/browse-edgar", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("CIK") != "S1" {
			t.Errorf("CIK = %q", r.URL.Query().Get("CIK"))
		}
		w.Write([]byte(`<feed><entry><content><accession-number>0000036405-26-000473</accession-number></content></entry></feed>`))
	})
	mux.HandleFunc("/Archives/edgar/data/36405/000003640526000473/primary_doc.xml", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "goldberry ") {
			t.Errorf("User-Agent = %q", r.UserAgent())
		}
		w.Write([]byte(nport))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s := NewSEC("me@example.com")
	s.Base = srv.URL

	h, err := s.Holdings(context.Background(), "VOO", 10)
	if err != nil || len(h.Top) != 3 || h.Top[0].Name != "Apple Inc." {
		t.Fatalf("holdings = %+v, %v", h, err)
	}
	if _, err := s.Holdings(context.Background(), "NOPE", 10); !errors.Is(err, ErrNoHoldings) {
		t.Errorf("unknown ticker: err = %v", err)
	}
}

func TestLatin1Feed(t *testing.T) {
	s := NewSEC("me@example.com")
	s.series = map[string]seriesRef{"ZZZ": {1, "S1"}}
	s.loaded = time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/cgi-bin/browse-edgar", func(w http.ResponseWriter, r *http.Request) {
		// EDGAR answers in ISO-8859-1; \xe9 is "é".
		w.Write([]byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\" ?><feed><name>Caf\xe9</name><entry><content><accession-number>0000000001-26-000001</accession-number></content></entry></feed>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s.Base = srv.URL
	acc, err := s.latestReport(context.Background(), seriesRef{1, "S1"})
	if err != nil || acc != "0000000001-26-000001" {
		t.Errorf("accession = %q, %v", acc, err)
	}
}
