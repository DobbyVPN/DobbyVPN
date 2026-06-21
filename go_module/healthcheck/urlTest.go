package healthcheck

import (
	hcCommon "go_module/healthcheck/common"
	"go_module/log"
	"net/http"
	"time"

	"github.com/matsuridayo/libneko/speedtest"
)

const (
	urlTestTimeoutMilliseconds = 1000
)

var httpClient = &http.Client{}

func URLTest(url string, standard int) (int32, error) {
	start := time.Now()
	log.Debugf(hcCommon.Category, "URLTest begin url=%s timeoutMs=%d standard=%d", url, urlTestTimeoutMilliseconds, standard)
	ret, err := speedtest.UrlTest(httpClient, url, urlTestTimeoutMilliseconds, standard)
	if err != nil {
		log.Debugf(hcCommon.Category, "URLTest failed url=%s elapsedMs=%d err=%v", url, time.Since(start).Milliseconds(), err)
		return ret, err
	}
	log.Debugf(hcCommon.Category, "URLTest OK url=%s retMs=%d elapsedMs=%d", url, ret, time.Since(start).Milliseconds())
	return ret, nil
}
