package registry

import (
	"io"
	"net/http"
	"path"
	"testing"

	"github.com/flant/k8s-image-availability-exporter/pkg/store"
	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseImageName(t *testing.T) {
	const (
		goodImageName                = "docker.io/test:test"
		goodImageNameWithoutRegistry = "test:test"
		badImageName                 = "te*^#@@st"

		defaultRegistryName = "test-registry.io"
	)

	_, err := parseImageName(goodImageName, "", false)
	require.NoError(t, err)

	_, err = parseImageName(badImageName, "", false)
	require.Error(t, err)

	ref, err := parseImageName(goodImageNameWithoutRegistry, defaultRegistryName, false)
	require.NoError(t, err)
	require.Equal(t, path.Join(defaultRegistryName, goodImageNameWithoutRegistry), ref.Name())
}

type rtFunc func(*http.Request) (*http.Response, error)
type MockRegistryTransport struct {
	RTFunc rtFunc
}

func (mr *MockRegistryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return mr.RTFunc(req)
}

func NewMockRegistryTransport(f rtFunc) *MockRegistryTransport {
	return &MockRegistryTransport{
		RTFunc: f,
	}
}

func Test_checkImageAvailability_mirrorReplacement(t *testing.T) {
	mirrorsMap := map[string]string{
		"index.docker.io": "index.mirror.local",
		"docker.io":       "mirror.local",
		"badhost.io":      "te*^#@@st.io",
	}

	tests := []struct {
		name                     string
		image                    string
		mirrorsMap               map[string]string      // Default nil
		responseStatus           int                    // Default http.StatusOK
		expectedHost             string                 // Default "" (Skip assertion if empty)
		expectedAvailabilityMode store.AvailabilityMode // Default store.Available
	}{
		{
			name:  "check statuses statusOk",
			image: "test:latest",
		},
		{
			name:  "check statuses statusOk library",
			image: "library/test:latest",
		},
		{
			name:  "check statuses statusOk sha256",
			image: "test@sha256:33e0bbc7ca9ecf108140af6288c7c9d1ecc77548cbfd3952fd8466a75edefe57",
		},
		{
			name:                     "check statuses notFound",
			image:                    "test:latest",
			responseStatus:           http.StatusNotFound,
			expectedAvailabilityMode: store.Absent,
		},
		{
			name:                     "check statuses unauthorized",
			image:                    "test:latest",
			responseStatus:           http.StatusUnauthorized,
			expectedAvailabilityMode: store.AuthnFailure,
		},
		{
			name:                     "check statuses forbidden",
			image:                    "test:latest",
			responseStatus:           http.StatusForbidden,
			expectedAvailabilityMode: store.AuthzFailure,
		},
		{
			name:                     "check statuses requestTimeout",
			image:                    "test:latest",
			responseStatus:           http.StatusRequestTimeout,
			expectedAvailabilityMode: store.UnknownError,
		},
		{
			name:                     "check statuses badImage",
			image:                    "te*^#@@st",
			expectedAvailabilityMode: store.BadImageName,
		},
		{
			name:                     "check statuses badImage sha256",
			image:                    "test@sha256:33e0bbc7ca9e",
			expectedAvailabilityMode: store.BadImageName,
		},
		{
			name:         "mirror replacement image name without repository",
			image:        "test:latest",
			mirrorsMap:   mirrorsMap,
			expectedHost: "mirror.local",
		},
		{
			name:         "mirror replacement image name with docker.io repository",
			image:        "docker.io/company/test:latest",
			mirrorsMap:   mirrorsMap,
			expectedHost: "mirror.local",
		},
		{
			name:         "mirror replacement image name with index.docker.io repository",
			image:        "index.docker.io/company/test:latest",
			mirrorsMap:   mirrorsMap,
			expectedHost: "index.mirror.local",
		},
		{
			name:         "mirror replacement host not in mirrors",
			image:        "test.io/test:latest",
			mirrorsMap:   mirrorsMap,
			expectedHost: "test.io",
		},
		{
			name:                     "mirror replacement image name with badhost repository",
			image:                    "badhost.io/test:latest",
			mirrorsMap:               mirrorsMap,
			expectedAvailabilityMode: store.BadImageName,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(tt *testing.T) {

			var actualHost string

			responseStatus := http.StatusOK
			if test.responseStatus != 0 {
				responseStatus = test.responseStatus
			}

			mockRoundTripFunc := func(req *http.Request) (*http.Response, error) {
				actualHost = req.Host

				return &http.Response{
					StatusCode: responseStatus,
					Body:       http.NoBody,
					Header: http.Header{
						"Content-Type":          {"application/vnd.docker.distribution.manifest.v2+json"},
						"Docker-Content-Digest": {"sha256:33e0bbc7ca9ecf108140af6288c7c9d1ecc77548cbfd3952fd8466a75edefe57"},
					},
				}, nil
			}

			logger := logrus.New()
			logger.Out = io.Discard

			rc := &Checker{
				config: registryCheckerConfig{
					defaultRegistry: "docker.io",
					mirrorsMap:      test.mirrorsMap,
					plainHTTP:       false,
				},
				registryTransport: NewMockRegistryTransport(mockRoundTripFunc),
			}

			actualMode := rc.checkImageAvailability(logrus.NewEntry(logger), test.image, nil)
			assert.Equal(tt, test.expectedAvailabilityMode, actualMode)

			if test.expectedHost != "" {
				assert.Equal(tt, test.expectedHost, actualHost)
			}
		})
	}
}
