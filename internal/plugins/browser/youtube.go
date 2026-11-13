package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Check verdicts.
const (
	// VerdictWorks means playback kept up with the wall clock at a usable quality.
	VerdictWorks = "works"
	// VerdictSlow means the page loaded but playback lags / buffers / drops frames.
	VerdictSlow = "slow"
	// VerdictBlocked means the network path prevented playback (DPI, bot check, media errors).
	VerdictBlocked = "blocked"
	// VerdictError means the measurement itself could not be performed.
	VerdictError = "error"
)

// Verdict thresholds. They are deliberately explicit so the verdict can be
// explained from the numbers in the report.
const (
	// worksRatio is the minimum "played seconds / wall seconds" for "works".
	worksRatio = 0.85
	// slowRatioBelow marks playback that is effectively blocked.
	blockedRatio = 0.15
	// maxDroppedPercent is the acceptable share of dropped frames.
	maxDroppedPercent = 10.0
	// minBufferAheadSec is the acceptable buffer cushion at the end of the check.
	minBufferAheadSec = 1.5
	// minVideoWidth is the lowest resolution considered "usable".
	minVideoWidth = 480
)

// mediaError mirrors HTMLMediaElement.error.
type mediaError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// networkFailure aggregates identical network failures.
type networkFailure struct {
	Host  string `json:"host"`
	Error string `json:"error"`
	Count int    `json:"count"`
}

// CheckMetrics are the objective numbers collected during a check.
type CheckMetrics struct {
	PlayedSec        float64 `json:"playedSec"`
	WallSec          float64 `json:"wallSec"`
	PlayedRatio      float64 `json:"playedRatio"`
	BufferedAheadSec float64 `json:"bufferedAheadSec"`
	MaxBufferedAhead float64 `json:"maxBufferedAheadSec"`
	DownloadedMB     float64 `json:"downloadedMb"`
	ThroughputKBps   float64 `json:"throughputKbps"`
	VideoWidth       int     `json:"videoWidth"`
	VideoHeight      int     `json:"videoHeight"`
	DroppedFrames    int64   `json:"droppedFrames"`
	TotalFrames      int64   `json:"totalFrames"`
	DroppedPercent   float64 `json:"droppedPercent"`
	ReadyState       int     `json:"readyState"`
	NetworkState     int     `json:"networkState"`
	Samples          int     `json:"samples"`
	AdDetected       bool    `json:"adDetected"`
	VideoFound       bool    `json:"videoFound"`
	PlayingSamples   int     `json:"playingSamples"`
}

// CheckResult is one stored YouTube check (shown in the UI and kept in history).
type CheckResult struct {
	ID              string           `json:"id"`
	StartedAt       time.Time        `json:"startedAt"`
	Profile         string           `json:"profile"`
	ProfileName     string           `json:"profileName"`
	Proxy           string           `json:"proxy"`
	URL             string           `json:"url"`
	Verdict         string           `json:"verdict"`
	Summary         string           `json:"summary"`
	Reasons         []string         `json:"reasons"`
	Metrics         CheckMetrics     `json:"metrics"`
	NetworkFailures []networkFailure `json:"networkFailures"`
	ConsoleErrors   []string         `json:"consoleErrors"`
	Report          string           `json:"report"`
}

// playbackSample is one per-second snapshot of the <video> element.
type playbackSample struct {
	HasVideo     bool        `json:"hasVideo"`
	T            float64     `json:"t"`
	Paused       bool        `json:"paused"`
	Ended        bool        `json:"ended"`
	ReadyState   int         `json:"readyState"`
	NetworkState int         `json:"networkState"`
	Duration     float64     `json:"duration"`
	Width        int         `json:"w"`
	Height       int         `json:"h"`
	Buffered     float64     `json:"bufferedAhead"`
	Dropped      int64       `json:"dropped"`
	Total        int64       `json:"total"`
	Ad           bool        `json:"ad"`
	PlayerState  int         `json:"playerState"`
	Err          *mediaError `json:"err"`
}

// sampleJS snapshots the video element state. It is intentionally defensive:
// a partially initialised player must not throw.
const sampleJS = `(() => {
	const v = document.querySelector('video');
	if (!v) return {hasVideo: false};
	let bufferedEnd = 0;
	try {
		for (let i = 0; i < v.buffered.length; i++) bufferedEnd = Math.max(bufferedEnd, v.buffered.end(i));
	} catch (e) {}
	const q = (v.getVideoPlaybackQuality ? v.getVideoPlaybackQuality() : null) || {};
	const player = document.querySelector('#movie_player');
	return {
		hasVideo: true,
		t: v.currentTime || 0,
		paused: !!v.paused,
		ended: !!v.ended,
		readyState: v.readyState,
		networkState: v.networkState,
		duration: isFinite(v.duration) ? v.duration : 0,
		w: v.videoWidth,
		h: v.videoHeight,
		bufferedAhead: Math.max(0, bufferedEnd - (v.currentTime || 0)),
		dropped: q.droppedVideoFrames || 0,
		total: q.totalVideoFrames || 0,
		ad: !!document.querySelector('.ad-showing'),
		playerState: (player && player.getPlayerState) ? player.getPlayerState() : -1,
		err: v.error ? {code: v.error.code, message: v.error.message || ''} : null
	};
})()`

// playProbeJS returns the coordinates of the player centre so the plugin can
// send a real (trusted) click, which browsers require for media playback.
const playProbeJS = `(() => {
	const v = document.querySelector('video');
	const player = document.querySelector('#movie_player') || v;
	let x = 0, y = 0;
	if (player) {
		const r = player.getBoundingClientRect();
		x = Math.round(r.left + r.width / 2);
		y = Math.round(r.top + r.height / 2);
	}
	return {hasVideo: !!v, x: x, y: y, paused: v ? v.paused : true};
})()`

// consentJS dismisses the EU consent dialog (best effort).
const consentJS = `(() => {
	const re = /^(accept all|i agree|agree to all|принять все|принять|согласен|согласиться)$/i;
	const els = Array.from(document.querySelectorAll('button, [role="button"]'));
	for (const el of els) {
		const label = ((el.innerText || '').trim() || (el.getAttribute('aria-label') || '').trim());
		if (re.test(label)) { el.click(); return label; }
	}
	return '';
})()`

// skipAdJS clicks the "skip ad" button when it is visible.
const skipAdJS = `(() => {
	const btn = document.querySelector('.ytp-ad-skip-button, .ytp-skip-ad-button, .ytp-ad-skip-button-modern, button.ytp-ad-skip-button-container');
	if (btn) { btn.click(); return true; }
	return false;
})()`

// playJS asks the player to start playback (fallback for the trusted click).
const playJS = `(() => {
	const v = document.querySelector('video');
	if (!v) return 'no-video';
	try {
		const p = v.play();
		if (p && typeof p.catch === 'function') p.catch(() => {});
		return 'play-called';
	} catch (e) {
		return 'error: ' + e;
	}
})()`

// pageFactsJS returns the visible text used for blocked-page detection.
const pageFactsJS = `(() => ({
	url: location.href,
	title: document.title || '',
	text: (((document.body && document.body.innerText) || '').slice(0, 6000))
}))()`

// botMarkers are substrings that indicate YouTube refused to serve the video.
var botMarkers = []string{
	"sign in to confirm",
	"confirm you're not a bot",
	"confirm you are not a bot",
	"this content isn't available",
	"this video is unavailable",
	"video unavailable",
	"playback isn't available",
	"unusual traffic",
	"something went wrong",
	"error 153",
	"войдите, чтобы подтвердить",
	"это видео недоступно",
}

// isGoogleHost reports whether the host belongs to the YouTube/Google media path.
func isGoogleHost(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range []string{"youtube.com", "youtu.be", "googlevideo.com", "ytimg.com", "ggpht.com", "google.com", "gstatic.com", "googleapis.com", "youtube-nocookie.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// hostOf extracts the host name from a URL.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// requestRef remembers what a DevTools request ID refers to.
type requestRef struct {
	Host  string
	Media bool
}

// netCollector aggregates network and console events for one check.
type netCollector struct {
	mu            sync.Mutex
	requests      map[string]requestRef
	googleReqs    int
	mediaBytes    int64
	failureOrder  []string
	failures      map[string]*networkFailure
	consoleErrors []string
}

// newNetCollector creates an empty collector.
func newNetCollector() *netCollector {
	return &netCollector{
		requests: make(map[string]requestRef),
		failures: make(map[string]*networkFailure),
	}
}

// attach registers DevTools event handlers for the given flat session.
func (c *netCollector) attach(client *cdpClient, sessionID string) func() {
	cancels := []func(){
		client.On("Network.requestWillBeSent", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				RequestID string `json:"requestId"`
				Type      string `json:"type"`
				Request   struct {
					URL string `json:"url"`
				} `json:"request"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			host := hostOf(event.Request.URL)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.requests[event.RequestID] = requestRef{
				Host:  host,
				Media: strings.EqualFold(event.Type, "Media"),
			}
			if isGoogleHost(host) {
				c.googleReqs++
			}
		}),
		client.On("Network.responseReceived", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				RequestID string `json:"requestId"`
				Type      string `json:"type"`
				Response  struct {
					URL      string `json:"url"`
					MimeType string `json:"mimeType"`
				} `json:"response"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			ref := c.requests[event.RequestID]
			ref.Host = hostOf(event.Response.URL)
			ref.Media = ref.Media || strings.EqualFold(event.Type, "Media") ||
				strings.HasPrefix(event.Response.MimeType, "video/") || strings.HasPrefix(event.Response.MimeType, "audio/")
			c.requests[event.RequestID] = ref
		}),
		client.On("Network.loadingFinished", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				RequestID         string  `json:"requestId"`
				EncodedDataLength float64 `json:"encodedDataLength"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			ref, ok := c.requests[event.RequestID]
			if ok && isGoogleHost(ref.Host) {
				c.mediaBytes += int64(event.EncodedDataLength)
			}
			delete(c.requests, event.RequestID)
		}),
		client.On("Network.loadingFailed", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				RequestID     string `json:"requestId"`
				ErrorText     string `json:"errorText"`
				BlockedReason string `json:"blockedReason"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			if event.ErrorText == "" && event.BlockedReason == "" {
				return // cancelled by the page itself, not a network failure
			}
			if isBenignNetError(event.ErrorText) {
				return // aborted/blocked-by-page requests say nothing about the network path
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			ref, ok := c.requests[event.RequestID]
			host := ref.Host
			if !ok || !isGoogleHost(host) {
				return
			}
			reason := event.ErrorText
			if reason == "" {
				reason = "blocked: " + event.BlockedReason
			}
			key := host + "|" + reason
			if failure, exists := c.failures[key]; exists {
				failure.Count++
				return
			}
			c.failures[key] = &networkFailure{Host: host, Error: reason, Count: 1}
			c.failureOrder = append(c.failureOrder, key)
		}),
		client.On("Runtime.consoleAPICalled", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				Type string `json:"type"`
				Args []struct {
					Value       any    `json:"value"`
					Description string `json:"description"`
				} `json:"args"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			if event.Type != "error" {
				return
			}
			parts := make([]string, 0, len(event.Args))
			for _, arg := range event.Args {
				if arg.Description != "" {
					parts = append(parts, arg.Description)
				} else if arg.Value != nil {
					parts = append(parts, fmt.Sprint(arg.Value))
				}
			}
			c.appendConsole("console: " + strings.Join(parts, " "))
		}),
		client.On("Log.entryAdded", func(sid string, params json.RawMessage) {
			if sid != sessionID {
				return
			}
			var event struct {
				Entry struct {
					Level string `json:"level"`
					Text  string `json:"text"`
					URL   string `json:"url"`
				} `json:"entry"`
			}
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			if event.Entry.Level != "error" {
				return
			}
			text := event.Entry.Text
			if event.Entry.URL != "" {
				text += " [" + event.Entry.URL + "]"
			}
			c.appendConsole("log: " + text)
		}),
	}

	return func() {
		for _, cancel := range cancels {
			cancel()
		}
	}
}

// appendConsole stores a console error (capped).
func (c *netCollector) appendConsole(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.consoleErrors) >= 10 {
		return
	}
	c.consoleErrors = append(c.consoleErrors, truncateRunes(message, 300))
}

// snapshot returns the aggregated failures and counters.
func (c *netCollector) snapshot() (failures []networkFailure, consoleErrors []string, googleReqs int, mediaBytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range c.failureOrder {
		failures = append(failures, *c.failures[key])
	}
	consoleErrors = append(consoleErrors, c.consoleErrors...)
	return failures, consoleErrors, c.googleReqs, c.mediaBytes
}

// isBenignNetError reports failures that are normal page behaviour and must not
// be interpreted as network blocking (media players abort requests all the time).
func isBenignNetError(errorText string) bool {
	switch errorText {
	case "net::ERR_ABORTED", "net::ERR_BLOCKED_BY_CLIENT", "net::ERR_BLOCKED_BY_RESPONSE":
		return true
	}
	lower := strings.ToLower(errorText)
	return strings.Contains(lower, "cancel") || strings.Contains(lower, "aborted")
}

// hasFailures reports whether any Google request failed.
func (c *netCollector) hasFailures() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.failures) > 0
}

// mediaErrors collects distinct video element errors across samples.
func mediaErrors(samples []playbackSample) []mediaError {
	seen := make(map[int]mediaError)
	order := make([]int, 0, 2)
	for _, s := range samples {
		if s.Err == nil {
			continue
		}
		if _, ok := seen[s.Err.Code]; !ok {
			seen[s.Err.Code] = *s.Err
			order = append(order, s.Err.Code)
		}
	}
	result := make([]mediaError, 0, len(order))
	for _, code := range order {
		result = append(result, seen[code])
	}
	return result
}

// runYouTubeCheck performs the automated playback check.
func (p *BrowserPlugin) runYouTubeCheck(ctx context.Context, profileID, rawURL string) (*CheckResult, error) {
	settings := p.snapshot()
	profile, ok := settings.Profile(profileID)
	if !ok {
		return nil, fmt.Errorf("unknown network profile %q", profileID)
	}
	if strings.TrimSpace(rawURL) == "" {
		rawURL = settings.CheckURL
	}
	target, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	result := &CheckResult{
		ID:          fmt.Sprintf("%d", time.Now().UnixNano()),
		StartedAt:   time.Now(),
		Profile:     profile.ID,
		Proxy:       profile.Proxy,
		ProfileName: profile.Name,
		URL:         target,
		Reasons:     []string{},
	}

	sess, err := p.manager.acquire(ctx, profile.ID)
	if err != nil {
		result.Verdict = VerdictError
		result.Summary = "Browser could not be started"
		result.Reasons = append(result.Reasons, err.Error())
		result.Report = buildReport(result)
		p.appendHistory(result)
		return result, nil
	}

	collector := newNetCollector()
	detach := collector.attach(sess.client, sess.sessionID)
	defer detach()

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.CheckDurationSec)*time.Second+settings.navigateTimeout()+15*time.Second)
	defer cancel()

	if _, err := sess.navigate(checkCtx, target); err != nil {
		fillCollector(result, collector)
		decideNavigationFailure(result, err)
		result.Report = buildReport(result)
		p.appendHistory(result)
		return result, nil
	}

	started := time.Now()
	playAttempt := startPlayback(checkCtx, sess)

	samples := samplePlayback(checkCtx, sess, time.Duration(settings.CheckDurationSec)*time.Second)
	wallSec := time.Since(started).Seconds()

	facts := capturePageFacts(checkCtx, sess)
	fillCollector(result, collector)

	result.Metrics = computeMetrics(samples, wallSec, collector, playAttempt)
	decide(result, samples, facts, playAttempt)
	result.Report = buildReport(result)
	p.appendHistory(result)

	return result, nil
}

// playAttempt describes how the check tried to start playback.
type playAttempt struct {
	Role      string `json:"role"`
	Found     bool   `json:"found"`
	Clicked   bool   `json:"clicked"`
	PlayCall  string `json:"playCall"`
	Consent   string `json:"consent"`
	SkipAdRun bool   `json:"skipAd"`
}

// startPlayback dismisses dialogs and starts playback with a trusted click.
func startPlayback(ctx context.Context, sess *session) playAttempt {
	attempt := playAttempt{Role: "player"}

	if consent, err := sess.evalString(ctx, consentJS); err == nil && consent != "" {
		attempt.Consent = consent
		time.Sleep(1500 * time.Millisecond)
	}

	var probe struct {
		HasVideo bool    `json:"hasVideo"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
		Paused   bool    `json:"paused"`
	}
	if err := sess.evalJSON(ctx, playProbeJS, &probe); err != nil {
		return attempt
	}
	attempt.Found = probe.HasVideo
	if !probe.HasVideo {
		return attempt
	}

	if probe.Paused {
		if err := sess.click(ctx, probe.X, probe.Y); err == nil {
			attempt.Clicked = true
			time.Sleep(1500 * time.Millisecond)
		}
	}

	var first playbackSample
	if err := sess.evalJSON(ctx, sampleJS, &first); err == nil && first.Paused {
		if playCall, err := sess.evalString(ctx, playJS); err == nil {
			attempt.PlayCall = playCall
			time.Sleep(1500 * time.Millisecond)
		}
	}

	// Skip pre-roll ads so the measurement reflects the actual video.
	var skipped bool
	if err := sess.evalJSON(ctx, skipAdJS, &skipped); err == nil {
		attempt.SkipAdRun = skipped
	}

	return attempt
}

// samplePlayback takes one sample per second for the given duration.
func samplePlayback(ctx context.Context, sess *session, duration time.Duration) []playbackSample {
	samples := make([]playbackSample, 0, int(duration.Seconds())+1)
	deadline := time.Now().Add(duration)

	for {
		var sample playbackSample
		if err := sess.evalJSON(ctx, sampleJS, &sample); err == nil {
			samples = append(samples, sample)
		}
		if time.Now().After(deadline) {
			return samples
		}
		select {
		case <-ctx.Done():
			return samples
		case <-time.After(time.Second):
		}
	}
}

// pageFacts captures the visible page text for blocked-page detection.
type pageFacts struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// capturePageFacts reads the page text (best effort).
func capturePageFacts(ctx context.Context, sess *session) pageFacts {
	var facts pageFacts
	_ = sess.evalJSON(ctx, pageFactsJS, &facts)
	return facts
}

// computeMetrics turns samples and network counters into reportable numbers.
func computeMetrics(samples []playbackSample, wallSec float64, collector *netCollector, attempt playAttempt) CheckMetrics {
	metrics := CheckMetrics{
		WallSec:    wallSec,
		Samples:    len(samples),
		VideoFound: attempt.Found,
	}

	var (
		firstT   float64
		lastT    float64
		gotTime  bool
		lastBuff float64
	)
	for _, s := range samples {
		if s.Ad {
			metrics.AdDetected = true
			continue
		}
		if !s.HasVideo {
			continue
		}
		metrics.VideoFound = true
		if !s.Paused {
			metrics.PlayingSamples++
		}
		if !gotTime {
			firstT, lastT = s.T, s.T
			gotTime = true
		} else if s.T > lastT {
			lastT = s.T
		}
		if s.Buffered > metrics.MaxBufferedAhead {
			metrics.MaxBufferedAhead = s.Buffered
		}
		lastBuff = s.Buffered
		metrics.VideoWidth, metrics.VideoHeight = s.Width, s.Height
		metrics.DroppedFrames, metrics.TotalFrames = s.Dropped, s.Total
		metrics.ReadyState, metrics.NetworkState = s.ReadyState, s.NetworkState
	}

	metrics.BufferedAheadSec = lastBuff
	if gotTime {
		metrics.PlayedSec = lastT - firstT
	}
	if wallSec > 0 {
		metrics.PlayedRatio = metrics.PlayedSec / wallSec
	}
	if metrics.TotalFrames > 0 {
		metrics.DroppedPercent = float64(metrics.DroppedFrames) / float64(metrics.TotalFrames) * 100
	}

	_, _, _, mediaBytes := collector.snapshot()
	metrics.DownloadedMB = float64(mediaBytes) / (1024 * 1024)
	if wallSec > 0 {
		metrics.ThroughputKBps = float64(mediaBytes) / 1024 / wallSec
	}
	return metrics
}

// isNetworkNavigationError reports whether a navigation failure came from the
// network layer (DPI reset, dead proxy, DNS) rather than from the plugin.
func isNetworkNavigationError(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "net::err_") {
		return true
	}
	for _, marker := range []string{
		"connection reset", "connection refused", "connection closed", "timed out", "timeout",
		"name not resolved", "proxy", "dns", "tunnel", "ssl", "certificate", "aborted",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// decideNavigationFailure classifies a check that never got a page.
func decideNavigationFailure(result *CheckResult, navErr error) {
	result.Metrics.VideoFound = false
	message := navErr.Error()

	if isNetworkNavigationError(message) || len(result.NetworkFailures) > 0 {
		result.Verdict = VerdictBlocked
		result.Summary = "Playback blocked: the page cannot be opened from the host"
		result.Reasons = append(result.Reasons,
			"navigation failed: "+message,
			"the network path (DPI filter, proxy or DNS) rejected the request")
		if len(result.NetworkFailures) == 0 {
			result.Reasons = append(result.Reasons, "no successful HTTP response was received from the page")
		}
		return
	}

	result.Verdict = VerdictError
	result.Summary = "Page could not be opened"
	result.Reasons = append(result.Reasons, message)
}

// decide fills verdict, summary and reasons from the collected data.
func decide(result *CheckResult, samples []playbackSample, facts pageFacts, attempt playAttempt) {
	metrics := &result.Metrics
	failures := result.NetworkFailures
	botPage := detectBlockedPage(facts)
	mediaErrs := mediaErrors(samples)

	switch {
	case !metrics.VideoFound:
		if len(failures) > 0 {
			result.Verdict = VerdictBlocked
			result.Summary = "Playback blocked: media requests from the host failed"
			result.Reasons = append(result.Reasons, "no <video> element appeared on the page",
				fmt.Sprintf("%d distinct network failures to Google hosts (see below)", len(failures)))
		} else if botPage != "" {
			result.Verdict = VerdictBlocked
			result.Summary = "Playback blocked: YouTube refused to serve the video"
			result.Reasons = append(result.Reasons, "page reports: "+botPage)
		} else {
			result.Verdict = VerdictError
			result.Summary = "No video element found on the page"
			result.Reasons = append(result.Reasons, "the page opened but does not contain a <video> element")
		}

	case metrics.PlayedRatio < blockedRatio:
		switch {
		case len(failures) > 0:
			result.Verdict = VerdictBlocked
			result.Summary = "Playback blocked: the video stream cannot be downloaded"
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("played %.1fs of %.1fs (%.0f%%)", metrics.PlayedSec, metrics.WallSec, metrics.PlayedRatio*100),
				"media requests to Google hosts failed, the DPI bypass is probably off or not used")
		case len(mediaErrs) > 0:
			result.Verdict = VerdictBlocked
			result.Summary = "Playback blocked: the player reported a media error"
			for _, e := range mediaErrs {
				result.Reasons = append(result.Reasons, fmt.Sprintf("media error code %d (%s)", e.Code, e.Message))
			}
		case botPage != "":
			result.Verdict = VerdictBlocked
			result.Summary = "Playback blocked: YouTube refused to serve the video"
			result.Reasons = append(result.Reasons, "page reports: "+botPage)
		case metrics.PlayingSamples == 0:
			result.Verdict = VerdictError
			result.Summary = "Playback did not start"
			result.Reasons = append(result.Reasons,
				"the <video> element exists but stayed paused for the whole measurement",
				"check the page in the screenshot above: it may need an extra click or a login")
		default:
			result.Verdict = VerdictSlow
			result.Summary = "Playback barely advances"
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("played %.1fs of %.1fs (%.0f%%)", metrics.PlayedSec, metrics.WallSec, metrics.PlayedRatio*100))
		}

	case metrics.PlayedRatio < worksRatio:
		result.Verdict = VerdictSlow
		result.Summary = "Playback lags behind the wall clock"
		result.Reasons = append(result.Reasons, fmt.Sprintf(
			"played %.1fs of %.1fs (%.0f%%), the stream is slower than real time",
			metrics.PlayedSec, metrics.WallSec, metrics.PlayedRatio*100))

	case metrics.VideoWidth > 0 && metrics.VideoWidth < minVideoWidth:
		result.Verdict = VerdictSlow
		result.Summary = "Plays, but at a very low resolution"
		result.Reasons = append(result.Reasons, fmt.Sprintf("video resolution is %dx%d", metrics.VideoWidth, metrics.VideoHeight))

	case metrics.DroppedPercent > maxDroppedPercent:
		result.Verdict = VerdictSlow
		result.Summary = "Plays, but drops frames"
		result.Reasons = append(result.Reasons, fmt.Sprintf("%.1f%% of frames were dropped", metrics.DroppedPercent))

	case metrics.BufferedAheadSec < minBufferAheadSec:
		result.Verdict = VerdictSlow
		result.Summary = "Plays, but the buffer stays empty"
		result.Reasons = append(result.Reasons, fmt.Sprintf("only %.1fs of video was buffered ahead", metrics.BufferedAheadSec))

	default:
		result.Verdict = VerdictWorks
		result.Summary = "Playback keeps up with the wall clock"
		result.Reasons = append(result.Reasons, fmt.Sprintf(
			"played %.1fs of %.1fs (%.0f%%) at %dx%d",
			metrics.PlayedSec, metrics.WallSec, metrics.PlayedRatio*100, metrics.VideoWidth, metrics.VideoHeight))
	}

	if len(failures) > 0 && result.Verdict != VerdictBlocked {
		result.Reasons = append(result.Reasons, fmt.Sprintf("%d request(s) to Google hosts failed during the check", len(failures)))
	}
	if metrics.AdDetected {
		result.Reasons = append(result.Reasons, "an ad was detected during the measurement")
	}
	if attempt.Consent != "" {
		result.Reasons = append(result.Reasons, "consent dialog was dismissed")
	}
	if len(result.ConsoleErrors) > 0 {
		result.Reasons = append(result.Reasons, fmt.Sprintf("%d console error(s) recorded", len(result.ConsoleErrors)))
	}
}

// detectBlockedPage returns the matched marker when the page says the video is unavailable.
func detectBlockedPage(facts pageFacts) string {
	haystack := strings.ToLower(facts.Title + "\n" + facts.Text)
	for _, marker := range botMarkers {
		if strings.Contains(haystack, marker) {
			return marker
		}
	}
	return ""
}

// fillCollector copies collector data into the result.
func fillCollector(result *CheckResult, collector *netCollector) {
	failures, consoleErrors, _, _ := collector.snapshot()
	result.NetworkFailures = failures
	result.ConsoleErrors = consoleErrors
}

// buildReport renders the copy/paste friendly text report.
func buildReport(result *CheckResult) string {
	var b strings.Builder
	m := result.Metrics

	fmt.Fprintf(&b, "Browser check — %s\n", result.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "Profile: %s — %s\n", result.ProfileName, describeProxy(result.Proxy))
	fmt.Fprintf(&b, "URL: %s\n", result.URL)
	fmt.Fprintf(&b, "Verdict: %s — %s\n", strings.ToUpper(result.Verdict), result.Summary)
	fmt.Fprintf(&b, "Played: %.1f s of %.1f s measured (%.0f%%)\n", m.PlayedSec, m.WallSec, m.PlayedRatio*100)
	fmt.Fprintf(&b, "Buffer: %.1f s ahead at the end (max %.1f s)\n", m.BufferedAheadSec, m.MaxBufferedAhead)
	fmt.Fprintf(&b, "Downloaded: %.2f MB of media (%.0f KB/s average)\n", m.DownloadedMB, m.ThroughputKBps)
	fmt.Fprintf(&b, "Video: %dx%d, readyState %d, playing samples %d/%d\n",
		m.VideoWidth, m.VideoHeight, m.ReadyState, m.PlayingSamples, m.Samples)
	fmt.Fprintf(&b, "Dropped frames: %d of %d (%.1f%%)\n", m.DroppedFrames, m.TotalFrames, m.DroppedPercent)

	if len(result.Reasons) > 0 {
		b.WriteString("Reasons:\n")
		for _, reason := range result.Reasons {
			fmt.Fprintf(&b, " - %s\n", reason)
		}
	}
	if len(result.NetworkFailures) > 0 {
		b.WriteString("Network failures:\n")
		for _, failure := range result.NetworkFailures {
			fmt.Fprintf(&b, " - %s: %s x%s\n", failure.Host, failure.Error, intToString(failure.Count))
		}
	}
	if len(result.ConsoleErrors) > 0 {
		b.WriteString("Console errors:\n")
		for _, entry := range result.ConsoleErrors {
			fmt.Fprintf(&b, " - %s\n", entry)
		}
	}
	return b.String()
}

// describeProxy turns a proxy URL into human readable text.
func describeProxy(proxy string) string {
	if strings.TrimSpace(proxy) == "" {
		return "direct host network"
	}
	return proxy
}

// appendHistory stores the result at the head of the plugin history.
func (p *BrowserPlugin) appendHistory(result *CheckResult) {
	st := p.storage()
	if st == nil {
		return
	}
	history := p.history()
	history = append([]CheckResult{*result}, history...)
	if len(history) > historyLimit {
		history = history[:historyLimit]
	}
	if err := st.SetJSON(p.Name(), historyKey, history); err != nil {
		p.logf("failed to store check history: %v", err)
	}
}

// history returns the stored check results (newest first).
func (p *BrowserPlugin) history() []CheckResult {
	st := p.storage()
	if st == nil {
		return []CheckResult{}
	}
	var history []CheckResult
	if err := st.GetJSON(p.Name(), historyKey, &history); err != nil {
		return []CheckResult{}
	}
	return history
}

// clearHistory removes the stored check results.
func (p *BrowserPlugin) clearHistory() error {
	st := p.storage()
	if st == nil {
		return nil
	}
	return st.Delete(p.Name(), historyKey)
}
