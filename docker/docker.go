package docker

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
	"github.com/docker/docker/api/types"
	"github.com/genuinetools/reg/registry"
)

func warnAboutDockerHub(ctx *ankh.ExecutionContext, registryDomain string) {
	if registryDomain == "docker.io" || registryDomain == "registry-1.docker.io" {
		ctx.Logger.Warnf("The docker.io API is closed and has known, breaking deviatons "+
		"from the open source docker registry API.")
	}
}

func ParseImage(ctx *ankh.ExecutionContext, image string) (string, string, error) {
	parsed, err := registry.ParseImage(image)
	if err != nil {
		return "", "", err
	}

	// We want the configured docker registry to be our default is none was passed, but
	// we can't tell the docker API which default registry to use. So if we find that
	// the image was parsed to contain the default domain, but the input string doesn't
	// explicitly contain that, we rewrite the domain to our configured default.
	if parsed.Domain == "docker.io" && !strings.HasPrefix(image, "docker.io") {

		// ...then fix up the domain to be our configured default
		parsed.Domain = ctx.AnkhConfig.Docker.Registry

		// The docker image parsing code also seems to append `library/` when the image
		// appears to come from an official domain. Strip that off unless the image provided
		// explicitly contains `library/`.
		if strings.HasPrefix(parsed.Path, "library/") && !strings.Contains(image, "library/") {
			parsed.Path = strings.TrimPrefix(parsed.Path, "library/")
		}
	}

	ctx.Logger.Debugf("ParseImage: '%v' => domain=%v Path=%v", image, parsed.Domain, parsed.Path)
	return parsed.Domain, parsed.Path, nil
}

func newRegistry(ctx *ankh.ExecutionContext, registryDomain string) (*registry.Registry, error) {
	if registryDomain == "" {
		registryDomain = ctx.AnkhConfig.Docker.Registry
	}
	if registryDomain == "" {
		return nil, fmt.Errorf("No registry could be determined from image, and no "+
			"default registry configured as `docker.registry`")
	}
	// Rewrite http docker io
	if strings.HasPrefix(registryDomain, "http://docker.io") {
		registryDomain = strings.Replace(registryDomain, "http://docker.io", "http://registry-1.docker.io", 1)
	}

	// Rewrite https docker.io
	if strings.HasPrefix(registryDomain, "https://docker.io") {
		registryDomain = strings.Replace(registryDomain, "https://docker.io", "https://registry-1.docker.io", 1)
	}

	auth := types.AuthConfig{
		ServerAddress: registryDomain,
	}

	return registry.New(auth, registry.Opt{
		Domain:   registryDomain,
		Insecure: false,
		Debug:    ctx.Verbose,
		SkipPing: false,
		NonSSL:   false,
		Timeout:  time.Duration(10 * time.Second),
	})
}

// TODO: Is descending actually descending here, or ascending?
func ListTags(ctx *ankh.ExecutionContext, registryDomain string, image string, descending bool) (string, error) {
	r, err := newRegistry(ctx, registryDomain)
	if err != nil {
		return "", err
	}

	tags, err := listTags(ctx, r, image, 0, descending)
	if err != nil {
		return "", err
	}

	return strings.Join(tags, "\n"), nil
}

func listTags(ctx *ankh.ExecutionContext, r *registry.Registry,
	image string, limit int, descending bool) ([]string, error) {
	tags, err := r.Tags(image)
	if err != nil {
		warnAboutDockerHub(ctx, r.Domain)
		return []string{}, err
	}

	if len(tags) == 0 {
		ctx.Logger.Warnf("No tags for image '%v' in registry '%v'. "+
			"Try `ankh image ls` for a list of images and tags.", image, r.Domain)
		return []string{}, nil
	}

	tags = util.FilterStringsContaining(tags, ctx.ImageTagFilter)

	sort.Slice(tags, func(i, j int) bool {
		lessThan := util.FuzzySemVerCompare(tags[i], tags[j])
		if descending {
			// The default sort order is ascending, but we want descending tag order.
			return !lessThan
		}
		return lessThan
	})

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}
	return tags, nil
}

func ListImages(ctx *ankh.ExecutionContext, registry string, numToShow int) (string, error) {
	r, err := newRegistry(ctx, registry)
	if err != nil {
		return "", err
	}

	catalog, err := r.Catalog("")
	if err != nil {
		warnAboutDockerHub(ctx, r.Domain)
		return "", err
	}

	if len(catalog) == 0 {
		ctx.Logger.Warnf("No images in catalog for registry '%v'", r.Domain)
		return "", nil
	}
	sort.Strings(catalog)

	type WorkItem struct {
		Image string
		Tags  []string
	}

	// Map image names to the list of tags that we fetch from the registry
	concurrency := 8
	doneChannel := make(chan(bool), concurrency)
	workChannel := make(chan(*WorkItem), concurrency)
	workItems := []*WorkItem{}

	for i := 0; i < concurrency; i++ {
		go func() {
			for {
				work, ok := <-workChannel
				if !ok {
					doneChannel <- true
					return
				}

				tags, err := listTags(ctx, r, work.Image, numToShow, true)
				if err != nil {
					ctx.Logger.Warnf("Could not list tags for image %v: %v", work.Image, err)
					work.Tags = []string{"ErrorSentinel"}
					continue
				}

				work.Tags = tags
			}
		}()
	}

	for _, image := range catalog {
		work := &WorkItem{Image: image}
		workItems = append(workItems, work)
		workChannel <- work
	}
	close(workChannel)
	for i := 0; i < concurrency; i++ {
		<-doneChannel
	}

	formatted := bytes.NewBufferString("")
	w := tabwriter.NewWriter(formatted, 0, 8, 8, ' ', 0)
	fmt.Fprintf(w, "NAME\tTAG(S)\n")
	for _, work := range workItems {
		fmt.Fprintf(w, "%v\t%v\n", work.Image, strings.Join(work.Tags, ", "))
	}
	w.Flush()

	return formatted.String(), nil
}
