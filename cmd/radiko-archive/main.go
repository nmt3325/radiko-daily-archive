package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	radiko "github.com/yyoshiki41/go-radiko"
)

const (
	jstName        = "Asia/Tokyo"
	radikoDateTime = "20060102150405"
)

type options struct {
	mode           string
	date           string
	area           string
	station        string
	stations       string
	planPath       string
	outputDir      string
	programTimeout time.Duration
}

type plan struct {
	Date         string        `json:"date"`
	AreaID       string        `json:"area_id"`
	DetectedArea string        `json:"detected_area"`
	GeneratedAt  time.Time     `json:"generated_at"`
	Stations     []planStation `json:"stations"`
}

type planStation struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Programs int    `json:"programs"`
}

type manifest struct {
	Date        string          `json:"date"`
	AreaID      string          `json:"area_id"`
	StationID   string          `json:"station_id"`
	StationName string          `json:"station_name"`
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
	Status    string `json:"status"`
	File      string `json:"file,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Error     string `json:"error,omitempty"`
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
	flag.StringVar(&o.area, "area", "JP13", "radiko area ID")
	flag.StringVar(&o.station, "station", "", "station ID for download mode")
	flag.StringVar(&o.stations, "stations", "", "comma-separated station IDs for plan mode")
	flag.StringVar(&o.planPath, "plan", "plan.json", "plan output path")
	flag.StringVar(&o.outputDir, "out", "output", "archive output root")
	flag.DurationVar(&o.programTimeout, "program-timeout", 90*time.Minute, "timeout per program")
	flag.Parse()

	if o.date == "" {
		return errors.New("-date is required")
	}
	if o.area == "" {
		return errors.New("-area is required")
	}
	broadcastDate, err := parseBroadcastDate(o.date)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch o.mode {
	case "plan":
		return createPlan(ctx, o, broadcastDate)
	case "download":
		if o.station == "" {
			return errors.New("-station is required in download mode")
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

func authenticatedClient(ctx context.Context, area string) (*radiko.Client, string, string, error) {
	client, err := radiko.New("")
	if err != nil {
		return nil, "", "", fmt.Errorf("create go-radiko client: %w", err)
	}
	detected := client.AreaID()

	mail := strings.TrimSpace(os.Getenv("RADIKO_MAIL"))
	password := os.Getenv("RADIKO_PASSWORD")
	if (mail == "") != (password == "") {
		return nil, "", detected, errors.New("RADIKO_MAIL and RADIKO_PASSWORD must either both be set or both be empty")
	}

	if mail != "" {
		login, err := client.Login(ctx, mail, password)
		if err != nil {
			return nil, "", detected, fmt.Errorf("radiko premium login: %w", err)
		}
		if login.StatusCode() != 200 {
			return nil, "", detected, fmt.Errorf("radiko premium login returned status %d", login.StatusCode())
		}
		client.SetAreaID(area)
	} else {
		if detected == "OUT" || detected == "" {
			return nil, "", detected, errors.New("radiko sees this runner outside Japan; configure RADIKO_PROXY_URL or use a Japan-based self-hosted runner")
		}
		if detected != area {
			return nil, "", detected, fmt.Errorf("runner is in %s but requested area is %s; configure premium credentials or choose the detected area", detected, area)
		}
	}

	token, err := client.AuthorizeToken(ctx)
	if err != nil {
		return nil, "", detected, fmt.Errorf("go-radiko authorization failed (detected area %q): %w", detected, err)
	}
	return client, token, detected, nil
}

func createPlan(ctx context.Context, o options, broadcastDate time.Time) error {
	client, _, detected, err := authenticatedClient(ctx, o.area)
	if err != nil {
		return err
	}
	stations, err := client.GetStations(ctx, broadcastDate)
	if err != nil {
		return fmt.Errorf("get stations for %s: %w", o.date, err)
	}

	wanted := parseIDSet(o.stations)
	found := make(map[string]bool)
	result := plan{
		Date:         o.date,
		AreaID:       o.area,
		DetectedArea: detected,
		GeneratedAt:  time.Now().UTC(),
	}
	for _, station := range stations {
		if len(wanted) > 0 && !wanted[station.ID] {
			continue
		}
		found[station.ID] = true
		programs := stationPrograms(station)
		if len(programs) == 0 {
			continue
		}
		result.Stations = append(result.Stations, planStation{
			ID: station.ID, Name: station.Name, Programs: len(programs),
		})
	}
	sort.Slice(result.Stations, func(i, j int) bool { return result.Stations[i].ID < result.Stations[j].ID })

	if len(wanted) > 0 {
		var missing []string
		for id := range wanted {
			if !found[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return fmt.Errorf("requested station IDs not found in %s: %s", o.area, strings.Join(missing, ", "))
		}
	}
	if len(result.Stations) == 0 {
		return fmt.Errorf("no stations with programs found for %s in %s", o.date, o.area)
	}
	if err := writeJSONAtomic(o.planPath, result); err != nil {
		return err
	}
	fmt.Printf("Planned %d stations for %s (%s); detected area: %s\n", len(result.Stations), o.date, o.area, detected)
	return nil
}

func downloadStation(ctx context.Context, o options, broadcastDate time.Time) error {
	client, token, _, err := authenticatedClient(ctx, o.area)
	if err != nil {
		return err
	}
	stations, err := client.GetStations(ctx, broadcastDate)
	if err != nil {
		return fmt.Errorf("get stations for %s: %w", o.date, err)
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
	about := "Private archive generated with github.com/yyoshiki41/go-radiko.\nFor personal, non-commercial use only. Respect radiko terms and applicable law.\n"
	if err := os.WriteFile(filepath.Join(stationDir, "ABOUT.txt"), []byte(about), 0o644); err != nil {
		return err
	}

	m := manifest{
		Date: o.date, AreaID: o.area, StationID: selected.ID, StationName: selected.Name,
		GeneratedAt: time.Now().UTC(),
	}
	manifestPath := filepath.Join(stationDir, "manifest.json")

	for index, program := range programs {
		result := programResult{
			Start: program.Ft, End: program.To, Title: strings.TrimSpace(program.Title),
			Performer: strings.TrimSpace(program.Pfm), Status: "failed",
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

		fileName := fmt.Sprintf("%s--%s--%s.aac", program.Ft, cleanName(selected.ID, 32), cleanName(result.Title, 96))
		outputPath := filepath.Join(stationDir, fileName)
		fmt.Printf("[%d/%d] %s %s — %s\n", index+1, len(programs), selected.ID, program.Ft, result.Title)

		downloadErr := downloadProgram(ctx, client, token, selected.ID, start, outputPath, o.programTimeout)
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
	fmt.Printf("Station complete: %s; downloaded=%d failed=%d\n", selected.ID, m.Succeeded, m.Failed)
	if m.Succeeded == 0 {
		return fmt.Errorf("all %d programs failed for %s", m.Failed, selected.ID)
	}
	if m.Failed > 0 {
		return fmt.Errorf("%d of %d programs failed for %s; see manifest.json", m.Failed, len(m.Programs), selected.ID)
	}
	return nil
}

func downloadProgram(parent context.Context, client *radiko.Client, token, stationID string, start time.Time, outputPath string, timeout time.Duration) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg is not installed")
	}
	if timeout <= 0 {
		timeout = 90 * time.Minute
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := os.Remove(outputPath + ".part"); err != nil && !os.IsNotExist(err) {
			return err
		}

		ctx, cancel := context.WithTimeout(parent, timeout)
		playlist, err := client.TimeshiftPlaylistM3U8(ctx, stationID, start)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("get timeshift playlist: %w", err)
		} else if strings.TrimSpace(playlist) == "" {
			cancel()
			lastErr = errors.New("go-radiko returned an empty timeshift playlist")
		} else {
			args := []string{
				"-nostdin", "-hide_banner", "-loglevel", "warning", "-y",
				"-rw_timeout", "30000000",
				"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
				"-headers", "X-Radiko-AuthToken: " + token + "\r\n",
				"-i", playlist,
				"-map", "0:a:0", "-vn", "-c:a", "copy", "-f", "adts",
				outputPath + ".part",
			}
			cmd := exec.CommandContext(ctx, "ffmpeg", args...)
			cmd.Env = os.Environ()
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err = cmd.Run()
			cancel()
			if err == nil {
				info, statErr := os.Stat(outputPath + ".part")
				if statErr == nil && info.Size() > 0 {
					if renameErr := os.Rename(outputPath+".part", outputPath); renameErr != nil {
						return renameErr
					}
					return nil
				}
				if statErr != nil {
					err = statErr
				} else {
					err = errors.New("ffmpeg produced an empty file")
				}
			}
			lastErr = fmt.Errorf("ffmpeg attempt %d: %w: %s", attempt, err, tail(stderr.String(), 2000))
		}

		if parent.Err() != nil {
			return parent.Err()
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}
	}
	_ = os.Remove(outputPath + ".part")
	return lastErr
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
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
		"## %s — %s (%s)\n\n- Downloaded: **%d**\n- Failed: **%d**\n\n",
		escapeMarkdown(m.StationName), m.Date, m.StationID, m.Succeeded, m.Failed,
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
