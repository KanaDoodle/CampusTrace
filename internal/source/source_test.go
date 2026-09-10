package source

import (
	"context"
	"strings"
	"testing"
)

func TestImportAndSSRF(t *testing.T) {
	rows, err := CSV(strings.NewReader("company,title,job_type,locations,source_id,text\nExample,Backend,FULL_TIME,Shanghai,manual,hello\n"))
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if _, err = JSON(strings.NewReader(`[{"company":"x","bad":1}]`)); err == nil {
		t.Fatal("unknown accepted")
	}
	r, err := (HTTPAdapter{Client: PublicClient()}).Fetch(context.Background(), "http://127.0.0.1:12379")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "HTTP_ERROR" {
		t.Fatal("private endpoint fetched", r)
	}
}
