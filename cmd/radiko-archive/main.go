package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	radiko "github.com/yyoshiki41/go-radiko"
)

const (
	jstName              = "Asia/Tokyo"
	radikoDateTime       = "20060102150405"
	regionIndexURL       = "https://radiko.jp/v3/station/region/full.xml"
	downloaderName       = "yt-dlp + yt-dlp-rajiko"
	defaultProgramTimout = 90 * time.Minute
)

type options struct {
	mode           string
	date           string
	area           string
	areas          string
	station        string
	stations       string
	planPath       string
	outputDir      string
	programTimeout time.Duration
}

type plan struct {
	Date       string        `json:"date"`
	Areas      []string      `json:"areas"`
	Downloader string        `json:"downloader"`
	Generated  time.Time     `json:"generated_at"`
	Stations   []planStation `json:"stations"`
}

type planStation struct {
	AreaID   string `json:"area_id"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Programs int    `json:"programs"`
}

type manifest struct {
	Date        string          `json:"date"`
	AreaID      string          `json:"area_id"`
	StationID   string          `json:"station_id"`
	StationName string          `json:"station_name"`
	Downloader  string          `json:"downloader"`
	GeneratedAt time.Time       `json:"generated_at"`
	Succeeded   int             `json:"succeeded"`
	Failed      int             `json:"failed"`
	Programs    []programResult `json:"programs"`
}

type programResult struct {
	Start     string `json:"start"`
	End       string `json:"end"`
	Title     string `json:"title"`
	Performer string `json:"performer,omitempty"`
	SourceURL string `json:"source_url"`
	Status    string `json:"status"`
	File      string `json:"file,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Error     string `json:"error,omitempty"`
}

type regionDocument struct {
	Stations []regionStation `xml:"stations>station"`
}

type regionStation struct {
	ID       string `xml:"id"`
	Name     string `xml:"name"`
	AreaID   string `xml:"area_id"`
	AreaFree string `xml:"areafree"`
	TimeFree string `xml:"timefree"`
}

type ytDLPAuth struct {
	args    []string
	cleanup func()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var o options
	flag.StringVar(&o.mode, "mode", "plan", "plan or download")
	flag.StringVar(&o.date, "date", "", "broadcast date in YYYY-MM-DD (JST)")
	flag.StringVar(&o.area, "area", "", "single radiko area ID for download mode")
	flag.StringVar(&o.areas, "areas", "all", "all or comma-separated area IDs for plan mode")
	flag.StringVar(&o.station, "station", "", "station ID for download mode")
	flag.StringVar(&o.stations, "stations", "", "comma-separated station IDs for plan mode")
	flag.StringVar(&o.planPath, "plan", "plan.json", "plan output path")
	flag.StringVar(&o.outputDir, "out", "output", "archive output root")
	flag.DurationVar(&o.programTimeout, "program-timeout", defaultProgramTimout, "timeout per program")
	flag.Parse()

	if o.date == "" {
		return errors.New("-date is required")
	}
	broadcastDate, err := parseBroadcastDate(o.date)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch o.mode {
	case "plan":
		if o.area != "" && (o.areas == "" || strings.EqualFold(o.areas, "all")) {
			o.areas = o.area
		}
		return createPlan(ctx, o, broadcastDate)
	case "download":
		if o.area == "" || o.station == "" {
			return errors.New("-area and -station are required in download mode")
		}
		return downloadStation(ctx, o, broadcastDate)
	default:
		return fmt.Errorf("unsupported -mode %q", o.mode)
	}
}

func parseBroadcastDate(value string) (time.Time, error) {
	location, err := time.LoadLocation(jstName)
	if err != nil {
		return time.Time{}, err
	}
	date, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: use YYYY-MM-DD: %w", value, err)
	}
	return date, nil
}

func createPlan(ctx context.Context, o options, broadcastDate time.Time) error {
	areas, err := parseAreas(o.areas)
	if err != nil {
		return err
	}
	areaSet := make(map[string]bool, len(areas))
	for _, area := range areas {
		areaSet[area] = true
	}
	wanted := parseIDSet(o.stations)

	regionStations, err := loadRegionStations(ctx)
	if err != nil {
		return err
	}

	selected := make([]regionStation, 0, len(regionStations))
	found := make(map[string]bool)
	for _, station := range regionStations {
		if station.AreaFree != "1" || station.TimeFree != "1" || !areaSet[station.AreaID] {
			continue
		}
		if len(wanted) > 0 && !wanted[station.ID] {
			continue
		}
		selected = append(selected, station)
		found[station.ID] = true
	}
	if len(wanted) > 0 {
		var missing []string
		for id := range wanted {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return fmt.Errorf("station IDs are not area-free/time-free in the selected areas: %s", strings.Join(missing, ", "))
		}
	}
	if len(selected) == 0 {
		return errors.New("no area-free/time-free stations matched the requested filters")
	}

	byArea := make(map[string][]regionStation)
	for _, station := range selected {
		byArea[station.AreaID] = append(byArea[station.AreaID], station)
	}

	client, err := radiko.New("")
	if err != nil {
		return fmt.Errorf("create go-radiko schedule client: %w", err)
	}
	result := plan{
		Date: o.date, Areas: areas, Downloader: downloaderName, Generated: time.Now().UTC(),
	}
	for _, area := range areas {
		areaStations := byArea[area]
		if len(areaStations) == 0 {
			continue
		}
		client.SetAreaID(area)
		schedule, err := client.GetStations(ctx, broadcastDate)
		if err != nil {
			return fmt.Errorf("get %s schedule for %s: %w", area, o.date, err)
		}
		scheduleByID := make(map[string]radiko.Station, len(schedule))
		for _, station := range schedule {
			scheduleByID[station.ID] = station
		}
		for _, station := range areaStations {
			scheduled, ok := scheduleByID[station.ID]
			if !ok {
				return fmt.Errorf("station %s is missing from the %s schedule", station.ID, area)
			}
			programs := stationPrograms(scheduled)
			if len(programs) == 0 {
				continue
			}
			result.Stations = append(result.Stations, planStation{
				AreaID: area, ID: station.ID, Name: station.Name, Programs: len(programs),
			})
		}
	}
	sort.Slice(result.Stations, func(i, j int) bool {
		if result.Stations[i].AreaID != result.Stations[j].AreaID {
			return areaNumber(result.Stations[i].AreaID) < areaNumber(result.Stations[j].AreaID)
		}
		return result.Stations[i].ID < result.Stations[j].ID
	})
	if len(result.Stations) == 0 {
		return fmt.Errorf("no station schedules found for %s", o.date)
	}
	if err := writeJSONAtomic(o.planPath, result); err != nil {
		return err
	}
	fmt.Printf("Planned %d nationwide area-free stations across %d areas for %s\n", len(result.Stations), len(areas), o.date)
	return nil
}

func downloadStation(ctx context.Context, o options, broadcastDate time.Time) error {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return errors.New("yt-dlp is not installed; install requirements.txt")
	}

	client, err := radiko.New("")
	if err != nil {
		return fmt.Errorf("create go-radiko schedule client: %w", err)
	}
	client.SetAreaID(o.area)
	stations, err := client.GetStations(ctx, broadcastDate)
	if err != nil {
		return fmt.Errorf("get stations for %s in %s: %w", o.date, o.area, err)
	}

	var selected *radiko.Station
	for i := range stations {
		if stations[i].ID == o.station {
			selected = &stations[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("station %s not found in %s", o.station, o.area)
	}
	programs := stationPrograms(*selected)
	if len(programs) == 0 {
		return fmt.Errorf("station %s has no programs for %s", o.station, o.date)
	}

	stationDir := filepath.Join(o.outputDir, o.date, o.area, cleanName(o.station, 32))
	if err := os.MkdirAll(stationDir, 0o755); err != nil {
		return err
	}
	about := "Private archive generated with github.com/yyoshiki41/go-radiko and github.com/garret1317/yt-dlp-rajiko.\nFor personal, non-commercial use only. Respect radiko terms and applicable law.\n"
	if err := os.WriteFile(filepath.Join(stationDir, "ABOUT.txt"), []byte(about), 0o644); err != nil {
		return err
	}

	auth, err := createYTDLPAuth()
	if err != nil {
		return err
	}
	defer auth.cleanup()

	m := manifest{
		Date: o.date, AreaID: o.area, StationID: selected.ID, StationName: selected.Name,
		Downloader: downloaderName, GeneratedAt: time.Now().UTC(),
	}
	manifestPath := filepath.Join(stationDir, "manifest.json")

	for index, program := range programs {
		sourceURL := fmt.Sprintf("rdk://%s-%s", selected.ID, program.Ft)
		result := programResult{
			Start: program.Ft, End: program.To, Title: strings.TrimSpace(program.Title),
			Performer: strings.TrimSpace(program.Pfm), SourceURL: sourceURL, Status: "failed",
		}
		if result.Title == "" {
			result.Title = "Untitled"
		}
		start, parseErr := time.ParseInLocation(radikoDateTime, program.Ft, broadcastDate.Location())
		if parseErr != nil {
			result.Error = sanitizeError(parseErr.Error())
			m.Failed++
			m.Programs = append(m.Programs, result)
			_ = writeJSONAtomic(manifestPath, m)
			continue
		}

		fileName := fmt.Sprintf("%s--%s--%s.m4a", program.Ft, cleanName(selected.ID, 32), cleanName(result.Title, 96))
		outputPath := filepath.Join(stationDir, fileName)
		fmt.Printf("[%d/%d] %s %s — %s\n", index+1, len(programs), selected.ID, program.Ft, result.Title)

		downloadErr := downloadProgram(ctx, selected.ID, start, outputPath, o.programTimeout, auth.args)
		if downloadErr != nil {
			result.Error = sanitizeError(downloadErr.Error())
			m.Failed++
			fmt.Fprintf(os.Stderr, "  failed: %s\n", result.Error)
		} else {
			info, statErr := os.Stat(outputPath)
			if statErr != nil {
				result.Error = sanitizeError(statErr.Error())
				m.Failed++
			} else {
				digest, hashErr := fileSHA256(outputPath)
				if hashErr != nil {
					result.Error = sanitizeError(hashErr.Error())
					m.Failed++
				} else {
					result.Status = "downloaded"
					result.File = fileName
					result.Bytes = info.Size()
					result.SHA256 = digest
					m.Succeeded++
				}
			}
		}
		m.Programs = append(m.Programs, result)
		m.GeneratedAt = time.Now().UTC()
		if err := writeJSONAtomic(manifestPath, m); err != nil {
			return err
		}
	}

	if err := appendGitHubSummary(m); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write step summary: %v\n", err)
	}
	fmt.Printf("Station complete: %s/%s; downloaded=%d failed=%d\n", o.area, selected.ID, m.Succeeded, m.Failed)
	if m.Succeeded == 0 {
		return fmt.Errorf("all %d programs failed for %s", m.Failed, selected.ID)
	}
	if m.Failed > 0 {
		return fmt.Errorf("%d of %d programs failed for %s; see manifest.json", m.Failed, len(m.Programs), selected.ID)
	}
	return nil
}

func downloadProgram(parent context.Context, stationID string, start time.Time, outputPath string, timeout time.Duration, authArgs []string) error {
	if timeout <= 0 {
		timeout = defaultProgramTimout
	}
	sourceURL := fmt.Sprintf("rdk://%s-%s", stationID, start.Format(radikoDateTime))
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		removeDownloadFiles(outputPath)
		ctx, cancel := context.WithTimeout(parent, timeout)
		args := []string{
			"--ignore-config",
			"--no-progress",
			"--newline",
			"--force-overwrites",
			"--socket-timeout", "30",
			"--retries", "10",
			"--fragment-retries", "10",
			"--retry-sleep", "fragment:exp=1:10",
			"--concurrent-fragments", "4",
			"--format", "bestaudio/best",
			"--output", outputPath,
		}
		args = append(args, authArgs...)
		args = append(args, sourceURL)

		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		cancel()
		if err == nil {
			if resolved, resolveErr := resolveDownloadedFile(outputPath); resolveErr == nil {
				if resolved != outputPath {
					if renameErr := os.Rename(resolved, outputPath); renameErr != nil {
						return renameErr
					}
				}
				return nil
			} else {
				err = resolveErr
			}
		}
		lastErr = fmt.Errorf("yt-dlp attempt %d: %w: %s", attempt, err, tail(output.String(), 3000))
		if parent.Err() != nil {
			return parent.Err()
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}
	}
	removeDownloadFiles(outputPath)
	return lastErr
}

func loadRegionStations(ctx context.Context) ([]regionStation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, regionIndexURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "radiko-daily-archive/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download nationwide station index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download nationwide station index: HTTP %s", resp.Status)
	}
	var document regionDocument
	if err := xml.NewDecoder(resp.Body).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode nationwide station index: %w", err)
	}
	if len(document.Stations) == 0 {
		return nil, errors.New("nationwide station index was empty")
	}
	return document.Stations, nil
}

func parseAreas(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "all") || value == "*" {
		areas := make([]string, 0, 47)
		for i := 1; i <= 47; i++ {
			areas = append(areas, fmt.Sprintf("JP%d", i))
		}
		return areas, nil
	}
	seen := make(map[string]bool)
	var areas []string
	for _, raw := range strings.Split(value, ",") {
		area := strings.ToUpper(strings.TrimSpace(raw))
		if !strings.HasPrefix(area, "JP") {
			return nil, fmt.Errorf("invalid area ID %q", raw)
		}
		number, err := strconv.Atoi(strings.TrimLeft(strings.TrimPrefix(area, "JP"), "0"))
		if err != nil || number < 1 || number > 47 {
			return nil, fmt.Errorf("invalid area ID %q; expected JP1 through JP47", raw)
		}
		area = fmt.Sprintf("JP%d", number)
		if !seen[area] {
			seen[area] = true
			areas = append(areas, area)
		}
	}
	sort.Slice(areas, func(i, j int) bool { return areaNumber(areas[i]) < areaNumber(areas[j]) })
	if len(areas) == 0 {
		return nil, errors.New("no areas were specified")
	}
	return areas, nil
}

func areaNumber(area string) int {
	number, _ := strconv.Atoi(strings.TrimPrefix(area, "JP"))
	return number
}

func createYTDLPAuth() (ytDLPAuth, error) {
	result := ytDLPAuth{cleanup: func() {}}
	mail := strings.TrimSpace(os.Getenv("RADIKO_MAIL"))
	password := os.Getenv("RADIKO_PASSWORD")
	if (mail == "") != (password == "") {
		return result, errors.New("RADIKO_MAIL and RADIKO_PASSWORD must either both be set or both be empty")
	}
	if mail == "" {
		return result, nil
	}
	if strings.ContainsAny(mail, "\r\n") || strings.ContainsAny(password, "\r\n") {
		return result, errors.New("radiko credentials must not contain newlines")
	}
	file, err := os.CreateTemp("", "rajiko-netrc-*")
	if err != nil {
		return result, err
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return result, err
	}
	_, writeErr := fmt.Fprintf(file, "machine rajiko\n  login %s\n  password %s\n", netrcQuote(mail), netrcQuote(password))
	closeErr := file.Close()
	if writeErr != nil {
		os.Remove(path)
		return result, writeErr
	}
	if closeErr != nil {
		os.Remove(path)
		return result, closeErr
	}
	result.args = []string{"--netrc", "--netrc-location", path}
	result.cleanup = func() { _ = os.Remove(path) }
	return result, nil
}

func netrcQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func resolveDownloadedFile(outputPath string) (string, error) {
	if info, err := os.Stat(outputPath); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return outputPath, nil
	}
	matches, _ := filepath.Glob(outputPath + "*")
	for _, match := range matches {
		if strings.HasSuffix(match, ".part") || strings.HasSuffix(match, ".ytdl") {
			continue
		}
		if info, err := os.Stat(match); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return match, nil
		}
	}
	return "", errors.New("yt-dlp did not produce a non-empty media file")
}

func removeDownloadFiles(outputPath string) {
	matches, _ := filepath.Glob(outputPath + "*")
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

func stationPrograms(station radiko.Station) []radiko.Prog {
	if len(station.Progs.Progs) > 0 {
		return station.Progs.Progs
	}
	return station.Scd.Progs.Progs
}

func parseIDSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, part := range strings.Split(value, ",") {
		if id := strings.TrimSpace(part); id != "" {
			result[id] = true
		}
	}
	return result
}

func cleanName(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	count := 0
	lastUnderscore := false
	for _, r := range value {
		if count >= maxRunes {
			break
		}
		unsafe := unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`<>:"/\\|?*`, r)
		if unsafe {
			if !lastUnderscore {
				b.WriteRune('_')
				count++
				lastUnderscore = true
			}
			continue
		}
		b.WriteRune(r)
		count++
		lastUnderscore = false
	}
	result := strings.Trim(b.String(), " ._")
	if result == "" {
		return "untitled"
	}
	return result
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sanitizeError(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(tail(value, 3000))
}

func tail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return "…" + value[len(value)-limit:]
}

func appendGitHubSummary(m manifest) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file,
		"## %s — %s / %s (%s)\n\n- Downloaded: **%d**\n- Failed: **%d**\n- Downloader: **%s**\n\n",
		escapeMarkdown(m.StationName), m.Date, m.AreaID, m.StationID, m.Succeeded, m.Failed, downloaderName,
	)
	if err != nil {
		return err
	}
	if m.Failed > 0 {
		if _, err := fmt.Fprintln(file, "| Start | Program | Error |\n|---|---|---|"); err != nil {
			return err
		}
		for _, program := range m.Programs {
			if program.Status == "failed" {
				if _, err := fmt.Fprintf(file, "| %s | %s | %s |\n", program.Start, escapeMarkdown(program.Title), escapeMarkdown(program.Error)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
