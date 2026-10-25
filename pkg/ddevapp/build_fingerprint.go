package ddevapp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	composeTypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/ddev/ddev/pkg/dockerutil"
	"github.com/ddev/ddev/pkg/versionconstants"
)

// Build contexts larger than this are built every time rather than hashed.
const (
	buildFingerprintMaxFiles = 2000
	buildFingerprintMaxBytes = 64 * 1024 * 1024
)

// buildFingerprintPath is where the fingerprint of the last successful build
// is kept.
func (app *DdevApp) buildFingerprintPath() string {
	return app.GetConfigPath(".build-fingerprint")
}

// buildFingerprint hashes everything the images built for services (or for
// every service with build: when services is empty) depend on: the build
// config, the files in each build context, and the layers of the base and the
// built images. ok is false when that can't be determined safely, for example
// because an image is missing, and the build must then run.
func (app *DdevApp) buildFingerprint(project *composeTypes.Project, services []string) (fingerprint string, ok bool) {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "ddev %s\n", versionconstants.DdevVersion)
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		service := project.Services[name]
		if service.Build == nil || (len(services) > 0 && !slices.Contains(services, name)) {
			continue
		}
		// A --no-cache build sets these on the project; they don't change what
		// the build produces.
		build := *service.Build
		build.NoCache, build.Pull = false, false
		buildConfig, err := json.Marshal(build)
		if err != nil {
			return "", false
		}
		_, _ = fmt.Fprintf(h, "service %s image %s build %s\n", name, service.Image, buildConfig)

		builtImage := dockerutil.ImageContentID(service.Image)
		if service.Image == "" || builtImage == "" {
			return "", false
		}
		_, _ = fmt.Fprintf(h, "built %s\n", builtImage)

		if service.Build.DockerfileInline != "" || !filepath.IsAbs(service.Build.Context) {
			return "", false
		}
		if !hashBuildContext(h, service.Build.Context) {
			return "", false
		}
		dockerfile := service.Build.Dockerfile
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		if !filepath.IsAbs(dockerfile) {
			dockerfile = filepath.Join(service.Build.Context, dockerfile)
		}
		baseImages, err := dockerfileBaseImages(dockerfile, service.Build.Args)
		if err != nil {
			return "", false
		}
		for _, base := range baseImages {
			content := dockerutil.ImageContentID(base)
			if content == "" {
				return "", false
			}
			_, _ = fmt.Fprintf(h, "base %s %s\n", base, content)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// hashBuildContext adds the path, mode, and content of every file under dir
// to h. It reports false when dir is too large to hash or can't be read.
func hashBuildContext(h hash.Hash, dir string) bool {
	files, size := 0, int64(0)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "file %s %s %d\n", filepath.ToSlash(rel), info.Mode(), info.Size())
		if !info.Mode().IsRegular() {
			if info.Mode()&fs.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(h, "link %s\n", target)
			}
			return nil
		}
		files++
		size += info.Size()
		if files > buildFingerprintMaxFiles || size > buildFingerprintMaxBytes {
			return fs.SkipAll
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(h, f)
		return err
	})
	return err == nil && files <= buildFingerprintMaxFiles && size <= buildFingerprintMaxBytes
}

var dockerfileVarPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// dockerfileBaseImages returns the images the FROM lines in dockerfile pull
// from, with build args and ARG defaults substituted. Earlier stages and
// scratch are left out. It fails on a FROM line it can't resolve.
func dockerfileBaseImages(dockerfile string, args composeTypes.MappingWithEquals) ([]string, error) {
	f, err := os.Open(dockerfile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vars := map[string]string{}
	stages := map[string]bool{}
	var images []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "ARG":
			name, value, hasDefault := strings.Cut(fields[1], "=")
			if arg, ok := args[name]; ok && arg != nil {
				vars[name] = *arg
			} else if hasDefault {
				vars[name] = strings.Trim(value, `"'`)
			}
		case "FROM":
			from := slices.DeleteFunc(fields[1:], func(s string) bool { return strings.HasPrefix(s, "--") })
			if len(from) == 0 {
				return nil, fmt.Errorf("unable to parse %q in %s", scanner.Text(), dockerfile)
			}
			image := dockerfileVarPattern.ReplaceAllStringFunc(from[0], func(v string) string {
				return vars[dockerfileVarPattern.FindStringSubmatch(v)[1]]
			})
			if image == "" || strings.Contains(image, "$") {
				return nil, fmt.Errorf("unable to resolve %q in %s", scanner.Text(), dockerfile)
			}
			if image != "scratch" && !stages[strings.ToLower(image)] {
				images = append(images, image)
			}
			if len(from) >= 3 && strings.EqualFold(from[1], "AS") {
				stages[strings.ToLower(from[2])] = true
			}
		}
	}
	return images, scanner.Err()
}
