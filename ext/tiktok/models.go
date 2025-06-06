package tiktok

type Response struct {
	AwemeDetails []AwemeDetails `json:"aweme_details"`
	StatusCode   int            `json:"status_code"`
	StatusMsg    string         `json:"status_msg"`
}

type Cover struct {
	Height    int64    `json:"height"`
	URI       string   `json:"uri"`
	URLList   []string `json:"url_list"`
	URLPrefix any      `json:"url_prefix"`
	Width     int64    `json:"width"`
}

type PlayAddr struct {
	DataSize int64    `json:"data_size"`
	FileCs   string   `json:"file_cs"`
	FileHash string   `json:"file_hash"`
	Height   int64    `json:"height"`
	URI      string   `json:"uri"`
	URLKey   string   `json:"url_key"`
	URLList  []string `json:"url_list"`
	Width    int64    `json:"width"`
}

type Image struct {
	DisplayImage *DisplayImage `json:"display_image"`
}

type DisplayImage struct {
	Height    int      `json:"height"`
	URI       string   `json:"uri"`
	URLList   []string `json:"url_list"`
	URLPrefix any      `json:"url_prefix"`
	Width     int      `json:"width"`
}

type ImagePostInfo struct {
	Images      []Image `json:"images"`
	MusicVolume float64 `json:"music_volume"`
	PostExtra   string  `json:"post_extra"`
	Title       string  `json:"title"`
}

type Video struct {
	CdnURLExpired   int64     `json:"cdn_url_expired"`
	Cover           Cover     `json:"cover"`
	Duration        int64     `json:"duration"`
	HasWatermark    bool      `json:"has_watermark"`
	Height          int64     `json:"height"`
	PlayAddr        *PlayAddr `json:"play_addr"`
	PlayAddrBytevc1 *PlayAddr `json:"play_addr_bytevc1"`
	PlayAddrH264    *PlayAddr `json:"play_addr_h264"`
	Width           int64     `json:"width"`
}

type AwemeDetails struct {
	AwemeID       string         `json:"aweme_id"`
	AwemeType     int            `json:"aweme_type"`
	Desc          string         `json:"desc"`
	Video         *Video         `json:"video"`
	ImagePostInfo *ImagePostInfo `json:"image_post_info"`
}
