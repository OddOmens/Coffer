package files

import (
	"path"
	"strings"
)

// Which files get generated previews from the media worker.

var stillExts = map[string]bool{
	// Photos browsers can't show (or Go can't thumbnail).
	".heic": true, ".heif": true, ".avif": true, ".tif": true, ".tiff": true, ".bmp": true, ".psd": true,
	// Camera RAW.
	".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".arw": true, ".raf": true, ".orf": true,
	".rw2": true, ".pef": true, ".srw": true, ".3fr": true, ".raw": true,
}

// browserVideo plays in every current browser as long as the codec is
// H.264 or VP9, which most files with these extensions use.
var browserVideo = map[string]bool{".mp4": true, ".webm": true, ".m4v": true}

var proxyExts = map[string]bool{
	".mov": true, ".mkv": true, ".avi": true, ".mxf": true, ".mts": true, ".m2ts": true, ".wmv": true,
	".flv": true, ".mpg": true, ".mpeg": true, ".3gp": true, ".mp4": true, ".webm": true, ".m4v": true,
}

func ext(name string) string { return strings.ToLower(path.Ext(name)) }

// IsVideo reports whether the media worker can read name as video.
func IsVideo(name string) bool { return proxyExts[ext(name)] }

// NeedsStill reports whether previews and thumbnails of name come from a
// generated JPEG: photos in formats browsers don't show, and video frames.
func NeedsStill(name string) bool { return stillExts[ext(name)] || IsVideo(name) }

// NeedsProxy reports whether name should get an H.264 copy for playback
// without being asked. Browser formats only get one on request, when the
// original turns out not to play (HEVC, ProRes in .mp4 and so on).
func NeedsProxy(name string) bool { return IsVideo(name) && !browserVideo[ext(name)] }
