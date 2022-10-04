// Package speaker implements playback of beep.Streamer values through physical speakers.
package speaker

import (
	"github.com/faiface/beep"
	"github.com/hajimehoshi/oto/v2"
	"github.com/pkg/errors"
	"io"
	"time"
)

const channelNum = 2
const bitDepthInBytes = 2
const bytesPerSample = bitDepthInBytes * channelNum

var (
	context           *oto.Context
	contextSampleRate beep.SampleRate
)

// Init initializes audio playback through speaker. Must be called before using this package.
func Init(sampleRate beep.SampleRate) error {
	if context != nil {
		return errors.New("speaker is already initialized")
	}

	var err error
	var ready chan struct{}
	context, ready, err = oto.NewContext(int(sampleRate), channelNum, bitDepthInBytes)
	if err != nil {
		return errors.Wrap(err, "failed to initialize speaker")
	}
	<-ready

	contextSampleRate = sampleRate

	return nil
}

// Suspend audio playback until Resume() is called.
//
// Suspend is concurrent-safe.
func Suspend() error {
	err := context.Suspend()
	return errors.Wrap(err, "failed to suspend speaker")
}

// Resume audio playback after it was suspended by Suspend().
//
// Resume is concurrent-safe.
func Resume() error {
	err := context.Resume()
	return errors.Wrap(err, "failed to resume speaker")
}

// Play starts playing all provided Streamers through the speaker.
// Each player will be closed automatically after the streamer ends.
//
// Play is a convenience function for (some) backwards compatibility.
// To have more control, use NewPlayer() instead.
func Play(s ...beep.Streamer) {
	for _, s := range s {
		p := NewPlayer(s)
		p.Play()
		p.autoClosePlayer = true
	}
}

// NewPlayer creates a Player from a Streamer. Player can be used to finely control playback
// of the stream. A player has an internal buffer that will be filled by pulling samples from
// Streamer and will be drained by the audio being played. So their position won't necessarily
// match exactly. If you want to clear the underlying buffer for some reasons e.g., you want to
// seek the position of s, call the player's Reset() function.
// NewPlayer is concurrent safe, however s cannot be used by multiple players.
func NewPlayer(s beep.Streamer) Player {
	r := newReaderFromStreamer(s)
	p := context.NewPlayer(r)
	return Player{
		//s: s,
		r: r,
		p: p,
	}
}

// sampleReader is a wrapper for beep.Streamer to implement io.Reader.
type sampleReader struct {
	s   beep.Streamer
	buf [][2]float64
	num int
}

func newReaderFromStreamer(s beep.Streamer) *sampleReader {
	return &sampleReader{
		s: s,
	}
}

// Read pulls samples from the reader and fills buf with the encoded
// samples. Read expects the size of buf be divisible by the length
// of a sample (= channel count * bit depth in bytes).
func (s *sampleReader) Read(buf []byte) (n int, err error) {
	// Read samples from streamer
	if len(buf)%bytesPerSample != 0 {
		return 0, errors.New("requested number of bytes do not align with the samples")
	}
	ns := len(buf) / bytesPerSample
	if len(s.buf) < ns {
		s.buf = make([][2]float64, ns)
	}
	ns, ok := s.s.Stream(s.buf[:ns])
	if !ok {
		if s.s.Err() != nil {
			return 0, errors.Wrap(s.s.Err(), "streamer returned error when requesting samples")
		}
		if ns == 0 {
			// TODO: close player if autoClosePlayer is set but only after all samples have been played.
			return 0, io.EOF
		}
	}
	s.num += ns

	// Convert samples to bytes
	for i := range s.buf[:ns] {
		for c := range s.buf[i] {
			val := s.buf[i][c]
			if val < -1 {
				val = -1
			}
			if val > +1 {
				val = +1
			}
			valInt16 := int16(val * (1<<15 - 1))
			low := byte(valInt16)
			high := byte(valInt16 >> 8)
			buf[i*bytesPerSample+c*bitDepthInBytes+0] = low
			buf[i*bytesPerSample+c*bitDepthInBytes+1] = high
		}
	}

	return ns * bytesPerSample, nil
}

// Player gives control over the playback of the Steamer.
type Player struct {
	//s beep.Streamer
	r               *sampleReader
	p               oto.Player
	autoClosePlayer bool
}

// Pause the player.
func (s *Player) Pause() {
	s.p.Pause()
}

// Play resumes playing after playback was paused.
func (s *Player) Play() {
	s.p.Play()
}

// IsPlaying reports whether this player is playing.
func (s *Player) IsPlaying() bool {
	return s.p.IsPlaying()
}

// Reset clears the underlying buffer and pauses its playing.
// This can be useful when you want to Seek() or make other
// modifications to the Streamer. In this case, call Reset()
// before seeking or other functions.
// Reset will also reset SamplesPlayed and DurationPlayed.
func (s *Player) Reset() {
	s.p.Reset()
	s.r.num = 0
}

// Volume returns the current volume in the range of [0, 1].
// The default volume is 1.
// TODO: allow changing/reading volume in other bases. See Beep's Volume effect.
func (s *Player) Volume() float64 {
	return s.p.Volume()
}

// SetVolume sets the current volume in the range of [0, 1].
func (s *Player) SetVolume(v float64) {
	s.p.SetVolume(v)
}

//// UnplayedBufferSize returns the byte size in the underlying buffer that is not played yet.
//func (s *Player) UnplayedBufferSize() int {
//	return s.p.UnplayedBufferSize()
//}

// UnplayedSamplesInBuffer returns the number of samples in the underlying buffer that is not played yet.
func (s *Player) UnplayedSamplesInBuffer() int {
	return s.p.UnplayedBufferSize() / bytesPerSample
}

// SamplesPlayed returns the total number of samples played through this player.
// If the player is in a paused state, this will not increase.
func (s *Player) SamplesPlayed() int {
	return s.r.num - s.UnplayedSamplesInBuffer()
}

// DurationPlayed returns the playtime of this player based on the samples played and
// the sample rate speaker was initialized with.
// If the player is in a paused state, this will not increase.
func (s *Player) DurationPlayed() time.Duration {
	return contextSampleRate.D(s.SamplesPlayed())
}

// Err returns an error if this player has an error.
func (s *Player) Err() error {
	return s.p.Err()
}

// Close the player and remove it from the speaker.
// Streamer is *not* closed.
func (s *Player) Close() error {
	return s.p.Close()
}
