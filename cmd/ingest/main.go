package main

import (
	"flag"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"io"
	"log"
	"os"
	"time"
)

func main() {
	file := flag.String("file", "", "JSON or CSV file; stdin if omitted")
	format := flag.String("format", "json", "json or csv")
	sourceID := flag.String("source", "manual", "preconfigured source ID (local operator only)")
	register := flag.Bool("register-source", false, "register the source ID using name/type/trust options")
	name := flag.String("name", "", "source name")
	kind := flag.String("type", "MANUAL", "source type")
	trust := flag.String("trust", "MANUAL", "OFFICIAL, THIRD_PARTY or MANUAL; operator-attested")
	fetch := flag.Bool("fetch", false, "fetch empty-text public URLs")
	limit := flag.Int("rate", 6, "source requests per minute")
	owner := flag.String("owner", "", "owner user ID for private manual ingestion")
	timezone := flag.String("timezone", "Asia/Shanghai", "source IANA timezone for date-only deadlines")
	adapter := flag.String("adapter", "", "public discovery adapter: lever, greenhouse, smartrecruiters, xiaohongshu, baidu, meituan, jd, netease, alibaba, bilibili, kuaishou, oppo, siemens, haier, lenovo, midea, byd, hikvision, qihoo360, sany, inovance, vivo, honor, sgm, ctrip, tencent, ths, cmbnt, netease_game, leihuo, ctyun, ctcloud, mihoyo, pingan_tech, pingan_oneconnect, pingan_wallet, cmcloud, cmiot, cmhome, gbits, hundsun, yuewen, tcl_digital, tcl_honghu, cec_software")
	tenant := flag.String("tenant", "", "public platform tenant identifier")
	flag.Parse()
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()
	if *register {
		if *adapter != "" {
			if _, e := source.PlatformURL(d.Source{Adapter: *adapter, Tenant: *tenant}); e != nil {
				log.Fatal(e)
			}
		}
		if err = app.Store.SaveSource(ctx, d.Source{Adapter: *adapter, Tenant: *tenant, RateLimit: *limit, ID: *sourceID, Name: *name, Type: *kind, Trust: *trust, OwnerID: *owner, Timezone: *timezone}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("source registered by local operator")
		return
	}
	var input io.Reader = os.Stdin
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		input = f
	}
	var rows []p.Ingest
	switch *format {
	case "csv":
		rows, err = source.CSV(input)
	case "json":
		rows, err = source.JSON(input)
	default:
		log.Fatal("format must be json or csv")
	}
	if err != nil {
		log.Fatal(err)
	}
	for _, row := range rows {
		row.SourceID = *sourceID
		if *fetch && row.URL != "" {
			ok, err := app.Queue.Allow(ctx, "source:"+*sourceID, *limit, time.Minute)
			if err != nil || !ok {
				log.Fatal("source rate limit reached; retry import later")
			}
			result, err := (source.HTTPAdapter{Client: source.PublicClient()}).Fetch(ctx, row.URL)
			if err != nil {
				log.Fatal(err)
			}
			row.Text, row.FetchStatus, row.HTTPStatus = result.Text, result.Status, result.HTTPStatus
		}
		var o d.Observation
		if *owner != "" {
			o, err = app.Store.IngestForUser(ctx, *owner, row)
		} else {
			o, err = app.Store.Ingest(ctx, row)
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(d.JSON(o))
	}
}
