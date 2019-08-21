package builder

import (
	"archive/tar"
	"fmt"
	"io"
	"path"
	"regexp"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver"
	"github.com/pkg/errors"

	"github.com/buildpack/pack/internal/archive"
	"github.com/buildpack/pack/style"
)

const defaultAPI = "0.1"
const defaultLifecycleVersion = "0.3.0"

var DefaultLifecycleDescriptor = LifecycleDescriptor{
	Info: LifecycleInfo{
		Version: &Version{*semver.MustParse(defaultLifecycleVersion)},
	},
	API: LifecycleAPI{
		PlatformVersion:  defaultAPI,
		BuildpackVersion: defaultAPI,
	},
}

type Blob interface {
	Open() (io.ReadCloser, error)
}

type lifecycle struct {
	descriptor LifecycleDescriptor
	Blob
}

//go:generate mockgen -package testmocks -destination testmocks/lifecycle.go github.com/buildpack/pack/builder Lifecycle
type Lifecycle interface {
	Blob
	Descriptor() LifecycleDescriptor
	Validate(expectedVersion *semver.Version) error
}

type LifecycleDescriptor struct {
	Info LifecycleInfo `toml:"lifecycle"`
	API  LifecycleAPI  `toml:"api"`
}

type LifecycleInfo struct {
	Version *Version `toml:"version" json:"version"`
}

type LifecycleAPI struct {
	PlatformVersion  string `toml:"platform" json:"platform"`
	BuildpackVersion string `toml:"buildpack" json:"buildpack"`
}

func (l *lifecycle) Descriptor() LifecycleDescriptor {
	return l.descriptor
}

func NewLifecycle(blob Blob) (Lifecycle, error) {
	br, err := blob.Open()
	if err != nil {
		return nil, errors.Wrap(err, "open lifecycle blob")
	}
	defer br.Close()

	var descriptor LifecycleDescriptor
	_, buf, err := archive.ReadTarEntry(br, "lifecycle.toml")

	//TODO: make lifecycle descriptor required after v0.4.0 release
	if err != nil && errors.Cause(err) == archive.ErrEntryNotExist {
		return &lifecycle{
			Blob:       blob,
			descriptor: DefaultLifecycleDescriptor}, nil
	} else if err != nil {
		return nil, errors.Wrap(err, "decode lifecycle descriptor")
	}
	_, err = toml.Decode(string(buf), &descriptor)
	if err != nil {
		return nil, errors.Wrap(err, "decoding descriptor")
	}

	lifecycle := &lifecycle{Blob: blob, descriptor: descriptor}

	if err = lifecycle.validateBinaries(); err != nil {
		return nil, errors.Wrap(err, "validating binaries")
	}

	return lifecycle, nil
}

var lifecycleBinaries = []string{
	"detector",
	"restorer",
	"analyzer",
	"builder",
	"exporter",
	"cacher",
	"launcher",
}

// Validate validates the lifecycle package. If a version is provided, it ensures that the version matches what is expected.
func (l *lifecycle) Validate(expectedVersion *semver.Version) error {
	if err := l.validateVersion(expectedVersion); err != nil {
		return errors.Wrap(err, "invalid lifecycle: version")
	}

	return nil
}

func (l *lifecycle) validateBinaries() error {
	rc, err := l.Open()
	if err != nil {
		return errors.Wrap(err, "create lifecycle blob reader")
	}
	defer rc.Close()
	regex := regexp.MustCompile(`^[^/]+/([^/]+)$`)
	headers := map[string]bool{}
	tr := tar.NewReader(rc)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Wrap(err, "failed to get next tar entry")
		}

		pathMatches := regex.FindStringSubmatch(path.Clean(header.Name))
		if pathMatches != nil {
			headers[pathMatches[1]] = true
		}
	}
	for _, p := range lifecycleBinaries {
		_, found := headers[p]
		if !found {
			return fmt.Errorf("did not find '%s' in tar", p)
		}
	}
	return nil
}

func (l *lifecycle) validateVersion(expectedVersion *semver.Version) error {
	if expectedVersion != nil && !l.Descriptor().Info.Version.Equal(expectedVersion) {
		return fmt.Errorf("lifecycle has version %s which does not match provided version %s", style.Symbol(l.descriptor.Info.Version.String()), style.Symbol(expectedVersion.String()))
	}
	return nil
}
