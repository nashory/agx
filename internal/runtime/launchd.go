package runtime

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const LaunchAgentLabel = RuntimeServiceLabel

type launchdPlist struct {
	XMLName xml.Name  `xml:"plist"`
	Version string    `xml:"version,attr"`
	Dict    plistDict `xml:"dict"`
}

type plistDict struct {
	Items []any `xml:",any"`
}

type plistKey string
type plistString string
type plistArray struct {
	Strings []plistString `xml:"string"`
}
type plistTrue struct{}

func (d plistDict) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "dict"
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, item := range d.Items {
		if err := e.Encode(item); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

func (a plistArray) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "array"
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, item := range a.Strings {
		if err := e.Encode(item); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

func (k plistKey) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	return e.EncodeElement(string(k), xml.StartElement{Name: xml.Name{Local: "key"}})
}

func (s plistString) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	return e.EncodeElement(string(s), xml.StartElement{Name: xml.Name{Local: "string"}})
}

func (plistTrue) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	return e.EncodeElement("", xml.StartElement{Name: xml.Name{Local: "true"}})
}

func LaunchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist"), nil
}

func LaunchdEnvironmentPath(currentPath string, agxPath string) string {
	var paths []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range paths {
			if existing == path {
				return
			}
		}
		paths = append(paths, path)
	}
	if agxPath != "" {
		add(filepath.Dir(agxPath))
	}
	for _, path := range filepath.SplitList(currentPath) {
		add(path)
	}
	for _, path := range []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	} {
		add(path)
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func RenderLaunchAgentPlist(agxPath string, envPath string) ([]byte, error) {
	if agxPath == "" {
		return nil, fmt.Errorf("agx path is required")
	}
	stdoutPath, stderrPath := RuntimeLogPaths()
	items := []any{
		plistKey("Label"), plistString(LaunchAgentLabel),
		plistKey("ProgramArguments"), plistArray{Strings: []plistString{plistString(agxPath), "runtime", "start"}},
		plistKey("RunAtLoad"), plistTrue{},
		plistKey("KeepAlive"), plistTrue{},
		plistKey("StandardOutPath"), plistString(stdoutPath),
		plistKey("StandardErrorPath"), plistString(stderrPath),
	}
	if envPath != "" {
		envItems := []any{plistKey("PATH"), plistString(envPath)}
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			envItems = append(envItems, plistKey("HOME"), plistString(home))
		}
		items = append(items, plistKey("EnvironmentVariables"), plistDict{Items: envItems})
	}
	plist := launchdPlist{Version: "1.0", Dict: plistDict{Items: items}}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")
	if err := encoder.Encode(plist); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
