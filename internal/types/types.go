package types

import "fmt"

const (
	StatusQueued    = "queued"
	StatusActive    = "active"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusError     = "error"
	StatusCanceled  = "canceled"

	CategoryVideo   = "video"
	CategoryAudio   = "audio"
	CategoryArchive = "archive"
	CategoryProgram = "program"
	CategoryOther   = "other"

	ProxyProtocolHTTP    = "http"
	ProxyProtocolHTTPS   = "https"
	ProxyProtocolSOCKS5  = "socks5"
	ProxyProtocolSOCKS5H = "socks5h"

	ProxyModeInherit = "inherit"
	ProxyModeDirect  = "direct"
	ProxyModeProfile = "profile"

	DefaultProxyTestURL = "https://api.ipify.org?format=json"
)

type Task struct {
	ID               string  `json:"id"`
	URL              string  `json:"url"`
	Title            string  `json:"title"`
	Engine           string  `json:"engine"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	Quality          string  `json:"quality"`
	Category         string  `json:"category"`
	Priority         int     `json:"priority"`
	Source           string  `json:"source"`
	GID              string  `json:"gid"`
	FilePath         string  `json:"filePath"`
	Dir              string  `json:"dir"`
	Error            string  `json:"error"`
	Progress         float64 `json:"progress"`
	Downloaded       int64   `json:"downloaded"`
	Total            int64   `json:"total"`
	Speed            int64   `json:"speed"`
	ETA              int64   `json:"eta"`
	Container        string  `json:"container"`
	Subtitles        bool    `json:"subtitles"`
	Playlist         bool    `json:"playlist"`
	Connections      int     `json:"connections"`
	Retries          int     `json:"retries"`
	ProxyMode        string  `json:"proxyMode,omitempty"`
	ProxyProfileID   string  `json:"proxyProfileId,omitempty"`
	ProxyProfileName string  `json:"proxyProfileName,omitempty"`
	Username         string  `json:"-"`
	Password         string  `json:"-"`
	Cookies          string  `json:"-"`
	BrowserCookies   string  `json:"-"`
	Referrer         string  `json:"referrer,omitempty"`
	UserAgent        string  `json:"userAgent,omitempty"`
	MimeType         string  `json:"mimeType,omitempty"`
	SuggestedName    string  `json:"suggestedName,omitempty"`
	SHA256           string  `json:"sha256"`
	MD5              string  `json:"md5"`
	CreatedAt        int64   `json:"createdAt"`
	StartedAt        int64   `json:"startedAt"`
	CompletedAt      int64   `json:"completedAt"`
	UpdatedAt        int64   `json:"updatedAt"`
}

type AddRequest struct {
	URL            string `json:"url"`
	Engine         string `json:"engine"`
	Quality        string `json:"quality"`
	Container      string `json:"container"`
	Category       string `json:"category"`
	Subtitles      bool   `json:"subtitles"`
	Playlist       bool   `json:"playlist"`
	Title          string `json:"title"`
	Source         string `json:"source"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Cookies        string `json:"cookies"`
	Referrer       string `json:"referrer"`
	UserAgent      string `json:"userAgent"`
	MimeType       string `json:"mimeType"`
	SuggestedName  string `json:"filename"`
	ProxyMode      string `json:"proxyMode,omitempty"`
	ProxyProfileID string `json:"proxyProfileId,omitempty"`
	AllowDuplicate bool   `json:"allowDuplicate"`
	Browser        bool   `json:"-"`
}

type AddResult struct {
	Task      Task `json:"task"`
	Duplicate bool `json:"duplicate"`
}

type Settings struct {
	DownloadDir              string   `json:"downloadDir"`
	MaxConcurrent            int      `json:"maxConcurrent"`
	Connections              int      `json:"connections"`
	Proxy                    string   `json:"proxy,omitempty"`
	ProxyDefaultID           string   `json:"proxyDefaultId,omitempty"`
	ProxyBypass              string   `json:"proxyBypass,omitempty"`
	ProxyTestURL             string   `json:"proxyTestUrl,omitempty"`
	ProxyFallbackIDs         []string `json:"proxyFallbackIds,omitempty"`
	ProxyAllowDirectFallback bool     `json:"proxyAllowDirectFallback,omitempty"`
	ClipboardWatch           bool     `json:"clipboardWatch"`
	AutoAddClipboard         bool     `json:"autoAddClipboard"`
	AutoStart                bool     `json:"autoStart"`
	AutoRetry                bool     `json:"autoRetry"`
	ShutdownOnComplete       bool     `json:"shutdownOnComplete"`
	ScheduleEnabled          bool     `json:"scheduleEnabled"`
	ScheduleStart            string   `json:"scheduleStart"`
	ScheduleStop             string   `json:"scheduleStop"`
	GitHubToken              string   `json:"githubToken"`
	NotifyOnComplete         bool     `json:"notifyOnComplete"`
	BrowserBridgeEnabled     bool     `json:"browserBridgeEnabled"`
	BrowserPort              int      `json:"browserPort"`
	BrowserCaptureMode       string   `json:"browserCaptureMode"`
	BrowserCaptureCategories []string `json:"browserCaptureCategories"`
	BrowserForwardCookies    bool     `json:"browserForwardCookies"`
}

type BridgeInfo struct {
	Enabled          bool   `json:"enabled"`
	Running          bool   `json:"running"`
	Address          string `json:"address"`
	Port             int    `json:"port"`
	Error            string `json:"error,omitempty"`
	ExtensionID      string `json:"extensionId"`
	ExtensionVersion string `json:"extensionVersion"`
	ExtensionFolder  string `json:"extensionFolder"`
}

type BrowserTaskStatus struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Speed      int64   `json:"speed"`
	ETA        int64   `json:"eta"`
	Category   string  `json:"category"`
	Error      string  `json:"error,omitempty"`
}

type Stats struct {
	Speed          int64 `json:"speed"`
	Active         int   `json:"active"`
	Queued         int   `json:"queued"`
	Paused         int   `json:"paused"`
	Completed      int   `json:"completed"`
	Errors         int   `json:"errors"`
	Total          int   `json:"total"`
	CompletedBytes int64 `json:"completedBytes"`
	DailyBytes     int64 `json:"dailyBytes"`
	MonthlyBytes   int64 `json:"monthlyBytes"`
	AvgSpeed       int64 `json:"avgSpeed"`
	PeakSpeed      int64 `json:"peakSpeed"`
	DiskTotal      int64 `json:"diskTotal"`
	DiskFree       int64 `json:"diskFree"`
	DiskUsed       int64 `json:"diskUsed"`
}

type SpeedPoint struct {
	Day   string `json:"day"`
	Speed int64  `json:"speed"`
}

type ToolsStatus struct {
	Aria2        string `json:"aria2"`
	Ytdlp        string `json:"ytdlp"`
	FFmpeg       string `json:"ffmpeg"`
	Ready        bool   `json:"ready"`
	MediaReady   bool   `json:"mediaReady"`
	TorrentReady bool   `json:"torrentReady"`
	DataDir      string `json:"dataDir"`
}

type ProxyProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"-"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	LoadError string `json:"-"`
}

func (p ProxyProfile) String() string {
	return fmt.Sprintf("ProxyProfile{id=%s name=%q %s://%s@%s:%d hasPassword=%t}",
		p.ID, p.Name, p.Protocol, p.Username, p.Host, p.Port, p.Password != "")
}

type ProxyProfileView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"hasPassword"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	LoadError   string `json:"loadError,omitempty"`
}

type ProxyProfileInput struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ProxyTestResult struct {
	OK         bool   `json:"ok"`
	LatencyMS  int64  `json:"latencyMs"`
	ExitIP     string `json:"exitIp,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorKind  string `json:"errorKind,omitempty"`
	TestedAt   int64  `json:"testedAt"`
	TestURL    string `json:"testUrl,omitempty"`
	ProfileID  string `json:"profileId,omitempty"`
	ProfileTag string `json:"profileTag,omitempty"`
}

type InitialState struct {
	Settings      Settings           `json:"settings"`
	ProxyProfiles []ProxyProfileView `json:"proxyProfiles"`
	Tasks         []Task             `json:"tasks"`
	Stats         Stats              `json:"stats"`
	SpeedHistory  []SpeedPoint       `json:"speedHistory"`
	Tools         ToolsStatus        `json:"tools"`
	Error         string             `json:"error,omitempty"`
	Warning       string             `json:"warning,omitempty"`
}

type GitHubRepo struct {
	FullName      string `json:"fullName"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	Description   string `json:"description"`
	Stars         int    `json:"stars"`
	Language      string `json:"language"`
	DefaultBranch string `json:"defaultBranch"`
	HTMLURL       string `json:"htmlUrl"`
	UpdatedAt     string `json:"updatedAt"`
}

type GitHubAsset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"downloadUrl"`
	ContentType string `json:"contentType"`
}

type GitHubRelease struct {
	ID          int64         `json:"id"`
	Tag         string        `json:"tag"`
	Name        string        `json:"name"`
	PublishedAt string        `json:"publishedAt"`
	HTMLURL     string        `json:"htmlUrl"`
	Assets      []GitHubAsset `json:"assets"`
}
