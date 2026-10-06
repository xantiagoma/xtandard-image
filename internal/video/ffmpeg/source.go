package ffmpeg

import "os"

// FileSource is a transcode source in a file (a spooled video, WP-121b):
// a *cache.FileBuffer. OpenTranscodeFile reads it through a duplicate of
// the file's descriptor.
type FileSource interface {
	// File is the file and the source's offset in it.
	File() (*os.File, int64)
	Len() int64
	Retain()
	Release()
}
