package ext

import (
	"fmt"
	"net/url"
	"strings"
	"sync"

	"govd/models"

	"github.com/pkg/errors"
)

var (
	maxRedirects = 5

	extractorsByHost map[string][]*models.Extractor
	extractorMapOnce sync.Once
)

func initExtractorMap() {
	extractorMapOnce.Do(func() {
		extractorsByHost = make(map[string][]*models.Extractor)
		for _, extractor := range List {
			if len(extractor.Host) > 0 {
				for _, domain := range extractor.Host {
					extractorsByHost[domain] = append(extractorsByHost[domain], extractor)
				}
			}
		}
	})
}

func CtxByURL(urlStr string) (*models.DownloadContext, error) {
	initExtractorMap()

	var redirectCount int
	currentURL := urlStr

	for redirectCount <= maxRedirects {
		parsedURL, err := url.Parse(currentURL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL: %w", err)
		}

		host := strings.TrimPrefix(parsedURL.Host, "www.")

		extractors := extractorsByHost[host]
		if len(extractors) == 0 {
			return nil, nil
		}

		var extractor *models.Extractor
		var matches []string
		var groups map[string]string

		for _, ext := range extractors {
			matches = ext.URLPattern.FindStringSubmatch(currentURL)
			if matches != nil {
				extractor = ext
				groupNames := ext.URLPattern.SubexpNames()
				groups = make(map[string]string)
				for i, name := range groupNames {
					if name != "" && i < len(matches) {
						groups[name] = matches[i]
					}
				}
				groups["match"] = matches[0]
				break
			}
		}

		if extractor == nil || matches == nil {
			return nil, nil
		}

		ctx := &models.DownloadContext{
			MatchedContentID:  groups["id"],
			MatchedContentURL: groups["match"],
			MatchedGroups:     groups,
			Extractor:         extractor,
		}

		if !extractor.IsRedirect {
			return ctx, nil
		}

		response, err := extractor.Run(ctx)
		if err != nil {
			return nil, err
		}
		if response.URL == "" {
			return nil, errors.New("no URL found in response")
		}

		currentURL = response.URL
		redirectCount++

		if redirectCount > maxRedirects {
			return nil, fmt.Errorf("exceeded maximum number of redirects (%d)", maxRedirects)
		}
	}

	return nil, fmt.Errorf("failed to extract from URL: %s", urlStr)
}

func ByCodeName(codeName string) *models.Extractor {
	for _, extractor := range List {
		if extractor.CodeName == codeName {
			return extractor
		}
	}
	return nil
}
