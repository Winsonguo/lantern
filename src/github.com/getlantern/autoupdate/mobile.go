package autoupdate

import (
	"compress/bzip2"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/blang/semver"
	"github.com/getlantern/flashlight/proxied"
	"github.com/getlantern/go-update"
)

var (
	updateStagingServer = "https://update-stage.getlantern.org/update"
	_httpClient         *http.Client
	httpClientMutex     sync.Mutex
)

type Updater interface {
	ShowProgress(string)
}

// passThru wraps an existing io.Reader.
type passThru struct {
	io.Reader
	Updater
	total    int64 // total bytes transferred
	length   int64 // Expected length
	progress float64
}

// Read 'overrides' the underlying io.Reader's Read method.
// This is the one that will be called by io.Copy(). We simply
// use it to keep track of byte counts and then forward the call.
func (pt *passThru) Read(p []byte) (int, error) {
	n, err := pt.Reader.Read(p)
	if n > 0 {
		pt.total += int64(n)
		percentage := float64(pt.total) / float64(pt.length) * float64(100)
		i := int(percentage / float64(10))
		is := fmt.Sprintf("%v", i)
		pt.Updater.ShowProgress(fmt.Sprintf("%d", int(percentage)))

		if percentage-pt.progress > 2 {
			fmt.Fprintf(os.Stderr, is)
			pt.progress = percentage
		}
	}

	return n, err
}

func doCheckUpdate(shouldProxy bool, version, URL string, publicKey []byte) (string, error) {

	log.Debugf("Checking for new mobile version; current version: %s", version)

	httpClient, err := proxied.GetHTTPClient(shouldProxy)
	if err != nil {
		log.Errorf("Could not get HTTP client to download update: %v", err)
		return "", err
	}

	// specify go-update should use our httpClient
	update.SetHttpClient(httpClient)

	res, err := checkUpdate(version, URL, publicKey)
	if err != nil {
		log.Errorf("Error checking for update for mobile: %v", err)
		return "", err
	}

	v, err := semver.Make(version)
	if err != nil {
		log.Errorf("Error checking for update; could not parse version number: %v", err)
		return "", err
	}

	if isNewerVersion(v, res.Version) {
		log.Debugf("Newer version of Lantern mobile available! %s", res.Version)
		return res.Url, nil
	}

	log.Debugf("No new version available!")
	return "", nil
}

func CheckMobileUpdate(shouldProxy bool, appVersion string) (string, error) {
	return doCheckUpdate(shouldProxy, appVersion,
		updateStagingServer, []byte(PackagePublicKey))
}

func handleError(err error, updater Updater) {
	log.Error(err)
}

// UpdateMobile downloads the latest APK from the given url to apkPath
// If proxyAddr is specified, the client proxies through the given HTTP proxy
// Updater is an interface for calling back to Java (whether to display download progress
// or show an error message)
func UpdateMobile(shouldProxy bool, url, apkPath string, updater Updater) string {
	var req *http.Request
	var res *http.Response

	log.Debugf("Attempting to download APK from %s", url)

	out, err := os.Create(apkPath)
	if err != nil {
		handleError(err, updater)
		return ""
	}
	defer out.Close()

	httpClient, err := proxied.GetHTTPClient(shouldProxy)
	if err != nil {
		handleError(err, updater)
		return ""
	}

	if req, err = http.NewRequest("GET", url, nil); err != nil {
		handleError(fmt.Errorf("Error downloading update: %v", err), updater)
		return ""
	}

	// ask for gzipped feed content
	req.Header.Add("Accept-Encoding", "gzip")

	if res, err = httpClient.Do(req); err != nil {
		handleError(fmt.Errorf("Error requesting update: %v", err), updater)
		return ""
	}

	defer res.Body.Close()

	readerpt := &passThru{Updater: updater,
		Reader: bzip2.NewReader(res.Body), length: res.ContentLength}

	_, err = io.Copy(out, readerpt)
	if err != nil {
		handleError(fmt.Errorf("Error copying update: %v", err), updater)
		return ""
	}

	return apkPath
}