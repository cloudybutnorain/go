package query

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"golang.org/x/exp/slices"
)

type Query struct {
	clickConn ch.Conn
}

// for use with CH .Select()
type QueryResult struct {
	WindowStart time.Time
	GroupKey    string
	Hits        uint64
	Bytes       uint64
}

// below should be const, but golang knows better
var VALIDGROUPBYS = []string{"Browser", "Os", "Device", "Country", "Path", "StatusCategory"}
var VALIDBUCKETBYS = []string{"hour", "day", "week", "month"}
var VALIDBOTS = []string{"true", "false"}

func (q Query) HandleQuery(out http.ResponseWriter, req *http.Request) {

	// todo, this is gross. there must be a better way of defining and validating API spec

	hostname := req.URL.Query().Get("hostname")
	if hostname == "" {
		http.Error(out, "Query param 'hostname' not provided, quitting", http.StatusBadRequest)
		return
	}

	unixStartStr := req.URL.Query().Get("start")
	if unixStartStr == "" {
		http.Error(out, "Query param 'start' not provided, quitting", http.StatusBadRequest)
		return
	}

	unixStart, err := strconv.Atoi(unixStartStr)
	if err != nil {
		http.Error(out, "Query param 'start' is not a valid int, quitting", http.StatusBadRequest)
		return
	}

	unixEndStr := req.URL.Query().Get("end")
	if unixEndStr == "" {
		http.Error(out, "Query param 'end' not provided, quitting", http.StatusBadRequest)
		return
	}

	unixEnd, err := strconv.Atoi(unixEndStr)
	if err != nil {
		http.Error(out, "Query param 'end' is not a valid int, quitting", http.StatusBadRequest)
		return
	}

	includeBots := req.URL.Query().Get("bots")
	if !slices.Contains(VALIDBOTS, includeBots) {
		http.Error(out, fmt.Sprintf("Invalid bots %s (try one of %v)", includeBots, VALIDBOTS), http.StatusBadRequest)
	}

	groupby := req.URL.Query().Get("groupby")
	if !slices.Contains(VALIDGROUPBYS, groupby) {
		http.Error(out, fmt.Sprintf("Invalid groupby %s (try one of %v)", groupby, VALIDGROUPBYS), http.StatusBadRequest)
	}

	bucketby := req.URL.Query().Get("bucketby")
	if !slices.Contains(VALIDBUCKETBYS, bucketby) {
		http.Error(out, fmt.Sprintf("Invalid bucketby %s (try one of %v)", bucketby, VALIDBUCKETBYS), http.StatusBadRequest)
	}

	timezone := req.URL.Query().Get("tz")
	if false {
		// todo, some actual validation here, hashtag sql injection
		http.Error(out, fmt.Sprintf("Invalid timezone %s", timezone), http.StatusBadRequest)
	}

	queryStr := q.BuildClickhouseQuery(hostname, includeBots, groupby, bucketby, timezone, unixStart, unixEnd)
	// if err != nil {
	// 	http.Error(out, fmt.Sprintf("Unable to create valid query for influxdb: %w", err), http.StatusBadRequest)
	// 	return
	// }

	log.Printf("Query to clickhouse: %s", queryStr)

	var result []QueryResult
	err = q.clickConn.Select(req.Context(), &result, queryStr)

	if err != nil {
		log.Printf("Query was unsuccessful: %v", err)
		http.Error(out, "Query was unsuccessful", http.StatusInternalServerError)
		return
	}

	log.Printf("Result from clickhouse: %+v", result)

	return
}

func (q Query) BuildClickhouseQuery(hostname, includeBots, groupby, bucketby, timezone string, unixStart, unixEnd int) string {

	var query strings.Builder

	query.WriteString("SELECT ")

	timeFunctionMap := map[string]string{
		"hour":  "toStartOfHour",
		"day":   "toStartOfDay",
		"week":  "toStartOfWeek",
		"month": "toStartOfMonth",
	}

	// the toDateTime is necessary here so we end up with times formatted per the client's TZ
	timeFn := timeFunctionMap[bucketby]
	query.WriteString(fmt.Sprintf("%s(toDateTime(Timestamp, '%s')) as WindowStart, ", timeFn, timezone))

	query.WriteString(fmt.Sprintf("%s as GroupKey, ", groupby))

	// a bit silly, but we only want to count PAGE loads as "hits"
	query.WriteString("COUNT(CASE WHEN FileType = 'Page' THEN 1 ELSE 0 END) as Hits, ")
	// but count the bytes as total because otherwise would be nonsense
	query.WriteString("SUM(BytesSent) as Bytes ")

	query.WriteString("FROM accesslog ")

	query.WriteString(fmt.Sprintf("WHERE Host = '%s' ", hostname))

	// the toDateTime might not be necessary here since we're supplying epoch ms, but shrug
	query.WriteString(fmt.Sprintf("AND Timestamp >= toDateTime(%d, '%s') ", unixStart, timezone))
	query.WriteString(fmt.Sprintf("AND Timestamp < toDateTime(%d, '%s') ", unixEnd, timezone))

	query.WriteString("GROUP BY WindowStart, GroupKey ")

	intervalFunctionMap := map[string]string{
		"hour":  "toIntervalHour",
		"day":   "toIntervalDay",
		"week":  "toIntervalWeek",
		"month": "toIntervalMonth",
	}

	interval := intervalFunctionMap[bucketby]
	query.WriteString(fmt.Sprintf("ORDER BY WindowStart ASC WITH FILL STEP %s(1)", interval))

	return query.String()
}

// func (i InfluxClient) BuildInfluxQuery() (string, error) {

// 	// if includeBots is true, then we want everything -- so no filter
// 	// todo -- isprobablybot is a string ?? should fix that
// 	if includeBots == "false" {
// 		query.WriteString(" ")
// 		query.WriteString(fmt.Sprintf("and isprobablybot = 'false'"))
// 	}

// 	return query.String(), nil
// }
