package tiktok

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"govd/enums"
	"govd/models"
	"govd/util"
)

const (
	apiHostname        = "api16-normal-c-useast1a.tiktokv.com"
	installationID     = "7127307272354596614"
	appName            = "musical_ly"
	appID              = "1233"
	appVersion         = "37.1.4"
	manifestAppVersion = "2023508030"
	packageID          = "com.zhiliaoapp.musically/" + manifestAppVersion
	appUserAgent       = packageID + " (Linux; U; Android 13; en_US; Pixel 7; Build/TD1A.220804.031; Cronet/58.0.2991.0)"
)

var HTTPClient = &http.Client{
	Transport: &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		QUICConfig: &quic.Config{
			MaxIncomingStreams: -1,
			EnableDatagrams:    true,
		},
	},
}

var VMExtractor = &models.Extractor{
	Name:       "TikTok VM",
	CodeName:   "tiktokvm",
	Type:       enums.ExtractorTypeSingle,
	Category:   enums.ExtractorCategorySocial,
	URLPattern: regexp.MustCompile(`https:\/\/((?:vm|vt|www)\.)?(vx)?tiktok\.com\/(?:t\/)?(?P<id>[a-zA-Z0-9]+)`),
	IsRedirect: true,

	Run: func(ctx *models.DownloadContext) (*models.ExtractorResponse, error) {
		location, err := util.GetLocationURL(ctx.MatchedContentURL, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get url location: %w", err)
		}
		return &models.ExtractorResponse{
			URL: location,
		}, nil
	},
}

var Extractor = &models.Extractor{
	Name:       "TikTok",
	CodeName:   "tiktok",
	Type:       enums.ExtractorTypeSingle,
	Category:   enums.ExtractorCategorySocial,
	URLPattern: regexp.MustCompile(`https?:\/\/((www|m)\.)?(vx)?tiktok\.com\/((?:embed|@[\w\.-]+)\/)?(v(ideo)?|p(hoto)?)\/(?P<id>[0-9]+)`),

	Run: func(ctx *models.DownloadContext) (*models.ExtractorResponse, error) {
		mediaList, err := MediaListFromAPI(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get media: %w", err)
		}
		return &models.ExtractorResponse{
			MediaList: mediaList,
		}, nil
	},
}

func MediaListFromAPI(ctx *models.DownloadContext) ([]*models.Media, error) {
	var mediaList []*models.Media

	details, err := GetVideoAPI(ctx.MatchedContentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get from api: %w", err)
	}
	caption := details.Desc
	isImageSlide := details.ImagePostInfo != nil
	if !isImageSlide {
		media := ctx.Extractor.NewMedia(
			ctx.MatchedContentID,
			ctx.MatchedContentURL,
		)
		media.SetCaption(caption)
		video := details.Video

		// generic PlayAddr
		if video.PlayAddr != nil {
			format, err := ParsePlayAddr(video, video.PlayAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to parse playaddr: %w", err)
			}
			media.AddFormat(format)
		}
		// hevc PlayAddr
		if video.PlayAddrBytevc1 != nil {
			format, err := ParsePlayAddr(video, video.PlayAddrBytevc1)
			if err != nil {
				return nil, fmt.Errorf("failed to parse playaddr: %w", err)
			}
			media.AddFormat(format)
		}
		// h264 PlayAddr
		if video.PlayAddrH264 != nil {
			format, err := ParsePlayAddr(video, video.PlayAddrH264)
			if err != nil {
				return nil, fmt.Errorf("failed to parse playaddr: %w", err)
			}
			media.AddFormat(format)
		}
		mediaList = append(mediaList, media)
	} else {
		images := details.ImagePostInfo.Images
		for _, image := range images {
			media := ctx.Extractor.NewMedia(
				ctx.MatchedContentID,
				ctx.MatchedContentURL,
			)
			media.SetCaption(caption)
			media.AddFormat(&models.MediaFormat{
				FormatID: "image",
				Type:     enums.MediaTypePhoto,
				URL:      image.DisplayImage.URLList,
			})
			mediaList = append(mediaList, media)
		}
	}
	return mediaList, nil
}

func GetVideoAPI(awemeID string) (*AwemeDetails, error) {
	apiURL := fmt.Sprintf(
		"https://%s/aweme/v1/multi/aweme/detail/",
		apiHostname,
	)
	queryParams, err := BuildAPIQuery()
	if err != nil {
		return nil, fmt.Errorf("failed to build api query: %w", err)
	}
	postData := BuildPostData(awemeID)

	req, err := http.NewRequest(
		http.MethodPost,
		apiURL,
		postData,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.URL.RawQuery = queryParams.Encode()
	req.Header.Set("User-Agent", appUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Argus", "")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var data *Response
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	videoData, err := FindVideoData(data, awemeID)
	if err != nil {
		return nil, fmt.Errorf("failed to find video data: %w", err)
	}
	return videoData, nil
}
