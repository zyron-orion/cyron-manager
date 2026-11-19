package types

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
)

type Task struct {
	ID          string  `json:"id"`
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	Engine      string  `json:"engine"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Quality     string  `json:"quality"`
	Category    string  `json:"category"`
	Priority    int     `json:"priority"`
	Source      string  `json:"source"`
	GID         string  `json:"gid"`
	FilePath    string  `json:"filePath"`
	Dir         string  `json:"dir"`
	Error       string  `json:"error"`
	Progress    float64 `json:"progress"`
	Downloaded  int64   `json:"downloaded"`
	Total       int64   `json:"total"`
	Speed       int64   `json:"speed"`
	ETA         int64   `json:"eta"`
	Container   string  `json:"container"`
	Subtitles   bool    `json:"subtitles"`
	Playlist    bool    `json:"playlist"`
	Connections int     `json:"connections"`
	Retries     int     `json:"retries"`
	Username    string  `json:"username"`
	Password    string  `json:"password"`
	Cookies     string  `json:"cookies"`
	SHA256      string  `json:"sha256"`
	MD5         string  `json:"md5"`
	CreatedAt   int64   `json:"createdAt"`
	StartedAt   int64   `json:"startedAt"`
	CompletedAt int64   `json:"completedAt"`
	UpdatedAt   int64   `json:"updatedAt"`
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
	AllowDuplicate bool   `json:"allowDuplicate"`
}

type AddResult struct {
	Task      Task `json:"task"`
	Duplicate bool `json:"duplicate"`
}

type Settings struct {
	DownloadDir          string `json:"downloadDir"`
	MaxConcurrent        int    `json:"maxConcurrent"`
	Connections          int    `json:"connections"`
	Proxy                string `json:"proxy"`
	ClipboardWatch       bool   `json:"clipboardWatch"`
	AutoAddClipboard     bool   `json:"autoAddClipboard"`
	AutoStart            bool   `json:"autoStart"`
	AutoRetry            bool   `json:"autoRetry"`
	ShutdownOnComplete   bool   `json:"shutdownOnComplete"`
	ScheduleEnabled      bool   `json:"scheduleEnabled"`
	ScheduleStart        string `json:"scheduleStart"`
	ScheduleStop         string `json:"scheduleStop"`
	GitHubToken          string `json:"githubToken"`
	NotifyOnComplete     bool   `json:"notifyOnComplete"`
	BrowserBridgeEnabled bool   `json:"browserBridgeEnabled"`
	BrowserToken         string `json:"browserToken"`
}

type BridgeInfo struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Address string `json:"address"`
	Error   string `json:"error,omitempty"`
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

type InitialState struct {
	Settings     Settings     `json:"settings"`
	Tasks        []Task       `json:"tasks"`
	Stats        Stats        `json:"stats"`
	SpeedHistory []SpeedPoint `json:"speedHistory"`
	Tools        ToolsStatus  `json:"tools"`
	Error        string       `json:"error,omitempty"`
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
