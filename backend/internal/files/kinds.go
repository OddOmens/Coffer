package files

import (
	"mime"
	"path"
	"strings"
)

var extraTypes = map[string]string{
	".mov": "video/quicktime", ".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".mxf": "application/mxf", ".avi": "video/x-msvideo",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".aif": "audio/aiff", ".aiff": "audio/aiff",
	".flac": "audio/flac", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml", ".heic": "image/heic", ".heif": "image/heif",
	".tif": "image/tiff", ".tiff": "image/tiff", ".psd": "image/vnd.adobe.photoshop", ".exr": "image/x-exr",
	".pdf": "application/pdf", ".zip": "application/zip", ".txt": "text/plain", ".md": "text/markdown",
	".csv": "text/csv", ".json": "application/json", ".xml": "text/xml", ".log": "text/plain",
	".srt": "text/plain", ".vtt": "text/vtt", ".html": "text/html", ".htm": "text/html",
	".js": "text/javascript", ".css": "text/css", ".jsx": "text/plain", ".ts": "text/plain",
	".yml": "text/plain", ".yaml": "text/plain", ".toml": "text/plain", ".ini": "text/plain",
	".aep": "application/vnd.adobe.aftereffects", ".prproj": "application/vnd.adobe.premiere",
	".ai": "application/postscript", ".ttf": "font/ttf", ".otf": "font/otf", ".woff2": "font/woff2",
	".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text", ".ods": "application/vnd.oasis.opendocument.spreadsheet",
	".odp":  "application/vnd.oasis.opendocument.presentation",
	".gdoc": "application/vnd.google-apps.document", ".gsheet": "application/vnd.google-apps.spreadsheet",
	".gslides": "application/vnd.google-apps.presentation",
}

// MimeType guesses a type from the extension only. Content is never sniffed.
func MimeType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			return mt
		}
	}
	return "application/octet-stream"
}

var kinds = map[string][]string{
	"image": {"jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "heic", "heif", "tif", "tiff", "bmp", "exr", "raw", "cr2", "cr3",
		"nef", "dng", "arw", "raf", "orf", "rw2", "pef", "srw", "3fr"},
	"video":        {"mp4", "mov", "m4v", "webm", "mkv", "avi", "mxf", "r3d", "braw", "mts", "m2ts", "wmv", "flv", "mpg", "mpeg", "3gp"},
	"audio":        {"mp3", "wav", "aif", "aiff", "flac", "m4a", "aac", "ogg"},
	"pdf":          {"pdf"},
	"document":     {"doc", "docx", "odt", "rtf", "pages", "gdoc"},
	"spreadsheet":  {"xls", "xlsx", "ods", "csv", "numbers", "gsheet"},
	"presentation": {"ppt", "pptx", "odp", "key", "gslides"},
	"text":         {"txt", "md", "log", "srt", "vtt"},
	"code":         {"json", "xml", "yml", "yaml", "toml", "ini", "js", "jsx", "ts", "tsx", "css", "html", "htm", "py", "sh", "go", "rs", "sql", "jsxbin"},
	"archive":      {"zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz"},
	"design":       {"aep", "aet", "prproj", "psd", "ai", "indd", "fig", "sketch", "blend", "c4d", "mogrt", "xd"},
	"font":         {"ttf", "otf", "woff", "woff2"},
}

var kindByExt = func() map[string]string {
	m := map[string]string{}
	for k, exts := range kinds {
		for _, e := range exts {
			m[e] = k
		}
	}
	return m
}()

// KindOf classifies a name for filters and icons.
func KindOf(name string, isDir bool) string {
	if isDir {
		return "folder"
	}
	if k, ok := kindByExt[strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")]; ok {
		return k
	}
	return "other"
}
