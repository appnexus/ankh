package docker

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
	"github.com/docker/docker/api/types"
	"github.com/genuinetools/reg/registry"
)

func newRegistry(ctx *ankh.ExecutionContext) (*registry.Registry, error) {
	if ctx.AnkhConfig.Docker.Registry == "" {
		return nil, fmt.Errorf("Missing DockerRegistryURL in AnkhConfig")
	}

	// TODO: This is an extermely not-generic assumption.
	auth := types.AuthConfig{
		ServerAddress: ctx.AnkhConfig.Docker.Registry,
	}

	return registry.New(auth, registry.Opt{
		Domain:   ctx.AnkhConfig.Docker.Registry,
		Insecure: false,
		Debug:    ctx.Verbose,
		SkipPing: false,
		NonSSL:   false,
		Timeout:  time.Duration(10 * time.Second),
	})
}

// TODO: Is descending actually descending here, or ascending?
func ListTags(ctx *ankh.ExecutionContext, image string, descending bool) (string, error) {
	r, err := newRegistry(ctx)
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
		return []string{}, err
	}

	if len(tags) == 0 {
		ctx.Logger.Warnf("No tags for image '%v' in registry '%v'. "+
			"Try `ankh docker images` for a list of images and tags.", image, ctx.AnkhConfig.Docker.Registry)
		return []string{}, nil
	}

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

func VerifyTag(ctx *ankh.ExecutionContext, image string, tagValue string) error {

	ctx.Logger.Debugf("Verifying tag %v for image %v.", tagValue, image)

	// Cannot verify tag without image name
	if image == "" {
		ctx.Logger.Debug("Cannot verify tag. Image name is not set on ankh.yaml in the chart")
		return nil
	}

	r, err := newRegistry(ctx)
	if err != nil {
		return err
	}

	tags, err := listTags(ctx, r, image, 0, true)
	if err != nil {
		return err
	}

	for _, t := range tags {
		if t == tagValue {
			return nil
		}
	}

	return fmt.Errorf("Docker tag %v does not exist", tagValue)
}

func ListImages(ctx *ankh.ExecutionContext, numToShow int) (string, error) {
	r, err := newRegistry(ctx)
	if err != nil {
		return "", err
	}

	catalog, err := r.Catalog("")
	if err != nil {
		return "", err
	}

	if len(catalog) == 0 {
		ctx.Logger.Warnf("No images in catalog for registry '%v'",
			ctx.AnkhConfig.Docker.Registry)
		return "", nil
	}
	sort.Strings(catalog)

	type Result struct {
		Image string
		Tags  []string
	}
	results := make([]Result, len(catalog))

	// Map image names to the list of tags that we fetch from the registry
	var mtx sync.Mutex
	var wg sync.WaitGroup
	for i, image := range catalog {
		wg.Add(1)
		go func(image string, result *Result) {
			defer wg.Done()
			tags, err := listTags(ctx, r, image, numToShow, true)
			if err != nil {
				ctx.Logger.Warnf("Could not list tags for image %v: %v", image, err)
				return
			}

			mtx.Lock()
			defer mtx.Unlock()
			*result = Result{
				Image: image,
				Tags:  tags,
			}
		}(image, &results[i])
	}
	wg.Wait()

	formatted := bytes.NewBufferString("")
	w := tabwriter.NewWriter(formatted, 0, 8, 8, ' ', 0)
	fmt.Fprintf(w, "NAME\tTAG(S)\n")
	for _, result := range results {
		fmt.Fprintf(w, "%v\t%v\n", result.Image, strings.Join(result.Tags, ", "))
	}
	w.Flush()

	return formatted.String(), nil
}
