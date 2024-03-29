package intake

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"github.com/mileusna/useragent"
	"github.com/oschwald/geoip2-golang"
	"zgo.at/isbot"
)

// TODO, settle on EXACTLY ONE shared value for when a tag can't be determined
// right now it's KINDA "Unknown"
// maybe empty str?

// Geo is an interface over the parts of geoip2 we need
// just set up this way so we can inject mocks in unit tests
type Geo interface {
	Country(net.IP) (*geoip2.Country, error)
}

// Tagger is responsible for turning a BunnyLog into a point for influx
// with all the necessary tags, timestamps, etc
type Tagger struct {
	geoClient Geo
}

func (t Tagger) Device(bunny BunnyLog) (string, string) {
	ua := useragent.Parse(bunny.UserAgent)
	if ua.Mobile {
		return "device", "Mobile"
	} else if ua.Tablet {
		return "device", "Tablet"
	} else if ua.Desktop {
		return "device", "Desktop"
	} else {
		return "device", "Unknown"
	}
}

func (t Tagger) Browser(bunny BunnyLog) (string, string) {
	ua := useragent.Parse(bunny.UserAgent)
	if ua.Name == "-" {
		return "browser", "Unknown"
	}
	return "browser", ua.Name
}

func (t Tagger) Os(bunny BunnyLog) (string, string) {
	ua := useragent.Parse(bunny.UserAgent)
	if ua.OS == "" {
		return "os", "Unknown"
	}
	return "os", ua.OS
}

func (t Tagger) Country(bunny BunnyLog) (string, string) {
	record, err := t.geoClient.Country(bunny.RemoteIp)
	if err != nil {
		log.Printf("Unable to get country for IP %v: %v", bunny.RemoteIp, err)
		return "country", "Unknown"
	}
	if record.Country.Names["en"] == "" {
		log.Printf("Country came back blank for IP %v", bunny.RemoteIp)
		return "country", "Unknown"
	}
	return "country", record.Country.Names["en"]
}

func (t Tagger) StatusCode(bunny BunnyLog) (string, string) {
	return "statuscode", bunny.StatusCode
}

func (t Tagger) StatusCategory(bunny BunnyLog) (string, string) {
	if len(bunny.StatusCode) != 3 {
		log.Printf("Can't get status category from weird code: %v", bunny.StatusCode)
		return "statuscategory", "Unknown"
	}
	return "statuscategory", string(bunny.StatusCode[0]) + "xx"
}

func (t Tagger) Path(bunny BunnyLog) (string, string) {
	return "path", bunny.Url.Path
}

func (t Tagger) Referrer(bunny BunnyLog) (string, string) {
	return "", ""
}

func (t Tagger) FileType(bunny BunnyLog) (string, string) {

	slashIndex := strings.LastIndex(bunny.Url.Path, "/")
	filename := bunny.Url.Path[(slashIndex + 1):]

	if filename == "" {
		return "filetype", "Page"
	}

	dotIndex := strings.LastIndex(filename, ".")

	if dotIndex == -1 {
		return "filetype", "Page"
	}

	switch t := filename[(dotIndex + 1):]; t {

	case "html":
		return "filetype", "Page"

	case "css":
		return "filetype", "Stylesheet"

	case "js":
		return "filetype", "Javascript"

	case "img", "jpg", "jpeg", "png", "ico", "gif", "svg", "heic":
		return "filetype", "Image"

	case "ttf", "otf", "woff", "woff2":
		return "filetype", "Font"

	case "txt", "csv", "pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx":
		return "filetype", "Document"

	case "zip", "gz", "rar", "iso", "tar", "lzma", "bz2", "7z", "z", "tgz":
		return "filetype", "Archive"

	case "mp3", "m4a", "wav", "ogg", "flac", "midi", "aac", "wma":
		return "filetype", "Audio"

	case "mpg", "mpeg", "avi", "mp4", "flv", "h264", "mov", "mk4", "mkv", "m4v":
		return "filetype", "Video"

	case "xml":
		return "filetype", "RSS Feed"

	default:
		return "filetype", "Unknown"
	}
}

func (t Tagger) IsProbablyBot(bunny BunnyLog) (string, string) {
	// similar to isbot's "Bot" implementation, but skips the "does the header
	// indicate this is a prefetch" check since we ain't got no headers
	BotNoHeader := func() isbot.Result {
		i := isbot.UserAgent(bunny.UserAgent)
		if i > 0 {
			return i
		}

		return isbot.IPRange(fmt.Sprintf("%s", bunny.RemoteIp))
	}

	res := BotNoHeader()
	return "isprobablybot", fmt.Sprintf("%v", isbot.Is(res))
}

func (t Tagger) Tags(bunny BunnyLog) map[string]string {

	tagFuncSlice := []func(bunny BunnyLog) (string, string){
		t.Device,
		t.Browser,
		t.Os,
		t.Country,
		t.StatusCode,
		t.StatusCategory,
		t.Path,
		t.Referrer,
		t.FileType,
		t.IsProbablyBot,
	}

	tags := map[string]string{}
	for _, f := range tagFuncSlice {
		name, val := f(bunny)
		tags[name] = val
	}

	return tags
}

func (t Tagger) Point(bunny BunnyLog) *write.Point {

	tags := t.Tags(bunny)

	return influxdb2.NewPoint(
		// metric name
		bunny.Url.Host,
		// tags
		tags,
		// fields
		map[string]interface{}{"hits": 1},
		// ts
		time.UnixMilli(bunny.Timestamp),
	)
}
