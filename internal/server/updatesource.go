package server

// Where a self-update's bytes actually come from.
//
// They used to come from the release's name-based URLs:
//
//	https://github.com/HEGADE/kunai/releases/download/nightly/kunai-linux-amd64
//
// with a comment asserting that CI overwrites those assets on every push, so the
// URL is always the latest. That assertion is wrong, and it cost a real evening.
// GitHub redirects a name-based asset URL to a CDN that caches BY URL, so for a
// window after CI replaces an asset the old bytes are still served. A nightly
// published at 23:36 was still handing out the previous build at 23:37.
//
// The checksum did not save it, and it is worth understanding why: checksums.txt
// was fetched from a name-based URL too, so the stale binary and the stale
// checksum came from the same cached generation and matched each other perfectly.
// A consistent stale pair passes every check you can make on the pair alone.
//
// So assets are resolved through the API instead. Every re-upload gets a NEW
// asset id, which means there is no shared URL left to cache, and both the binary
// and its checksum come from ONE read of the release, so they cannot be from
// different generations even in principle.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// releaseAPI is the API endpoint describing the release this build updates from.
// A var so tests can point it at a local server.
var releaseAPI = channelReleaseAPI()

func channelReleaseAPI() string {
	if buildChannel == "nightly" {
		// The moving pre-release CI refreshes on every push to the branch.
		return "https://api.github.com/repos/HEGADE/kunai/releases/tags/nightly"
	}
	return "https://api.github.com/repos/HEGADE/kunai/releases/latest"
}

// releaseAsset is one downloadable file in a release.
type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// URL is the API URL for this asset, which embeds the id and therefore
	// changes whenever CI re-uploads. This is the field that defeats the cache.
	URL string `json:"url"`
}

// release is the subset of GitHub's release payload the updater needs.
type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// find returns the asset with this name.
func (r release) find(name string) (releaseAsset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("the release has no %s (assets: %s)", name, r.assetNames())
}

func (r release) assetNames() string {
	names := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// fetchRelease reads the release once, so every asset taken from it belongs to
// the same generation. That is the property the old name-based fetch could not
// offer, and its absence is what let a stale binary match a stale checksum.
func fetchRelease(client *http.Client) (release, error) {
	req, err := http.NewRequest(http.MethodGet, releaseAPI, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("reading the release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("reading the release: HTTP %d", resp.StatusCode)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("reading the release: %w", err)
	}
	if len(rel.Assets) == 0 {
		return release{}, fmt.Errorf("the release has no assets yet")
	}
	return rel, nil
}

// openAsset starts a download of one asset by its id.
//
// Accept: application/octet-stream is what turns the API URL into the bytes
// rather than the JSON describing them.
func openAsset(client *http.Client, a releaseAsset) (*http.Response, error) {
	url := a.URL
	if url == "" {
		return nil, fmt.Errorf("asset %s has no download URL", a.Name)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: HTTP %d", a.Name, resp.StatusCode)
	}
	return resp, nil
}
