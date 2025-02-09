package bytesize_test

import (
	"testing"

	"github.com/bwagner5/vm/pkg/bytesize"
	"github.com/samber/lo"
)

func TestByteSize(t *testing.T) {
	type testCase struct {
		// required expectations

		val            string
		expectedBytes  int64
		expectedString string

		// optional expectations

		expectedKilobytes *float64
		expectedKibibytes *float64
		expectedMegabytes *float64
		expectedMebibytes *float64
		expectedGigabytes *float64
		expectedGibibytes *float64
		expectedTerabytes *float64
		expectedTebibytes *float64
		expectedPetabytes *float64
		expectedPebibytes *float64
		expectedExabytes  *float64
		expectedExbibytes *float64

		expectErr bool
	}
	for _, tc := range []testCase{
		{
			val:            "1",
			expectedBytes:  1,
			expectedString: "1 B",
		},
		{
			val:               "1K",
			expectedBytes:     1000,
			expectedKilobytes: lo.ToPtr(1.0),
			expectedString:    "1 KB",
		},
		{
			val:               "1Ki",
			expectedBytes:     1024,
			expectedKibibytes: lo.ToPtr(1.0),
			expectedString:    "1 KiB",
		},
		{
			val:               "1M",
			expectedBytes:     1_000_000,
			expectedMegabytes: lo.ToPtr(1.0),
			expectedString:    "1 MB",
		},
		{
			val:               "1Mi",
			expectedBytes:     1_048_576,
			expectedMebibytes: lo.ToPtr(1.0),
			expectedString:    "1 MiB",
		},
		{
			val:               "1G",
			expectedBytes:     1_000_000_000,
			expectedGigabytes: lo.ToPtr(1.0),
			expectedString:    "1 GB",
		},
		{
			val:               "1Gi",
			expectedBytes:     1_073_741_824,
			expectedGibibytes: lo.ToPtr(1.0),
			expectedString:    "1 GiB",
		},
		{
			val:               "1T",
			expectedBytes:     1_000_000_000_000,
			expectedTerabytes: lo.ToPtr(1.0),
			expectedString:    "1 TB",
		},
		{
			val:               "1Ti",
			expectedBytes:     1_099_511_627_776,
			expectedTebibytes: lo.ToPtr(1.0),
			expectedString:    "1 TiB",
		},
		{
			val:               "1P",
			expectedBytes:     1_000_000_000_000_000,
			expectedPetabytes: lo.ToPtr(1.0),
			expectedString:    "1 PB",
		},
		{
			val:               "1Pi",
			expectedBytes:     1_125_899_906_842_624,
			expectedPebibytes: lo.ToPtr(1.0),
			expectedString:    "1 PiB",
		},
		{
			val:              "1E",
			expectedBytes:    1_000_000_000_000_000_000,
			expectedExabytes: lo.ToPtr(1.0),
			expectedString:   "1 EB",
		},
		{
			val:               "1Ei",
			expectedBytes:     1_152_921_504_606_846_976,
			expectedExbibytes: lo.ToPtr(1.0),
			expectedString:    "1 EiB",
		},
		{
			val:               "1 KiB",
			expectedBytes:     1024,
			expectedKibibytes: lo.ToPtr(1.0),
			expectedString:    "1 KiB",
		},
		{
			val:       "1h",
			expectErr: true,
		},
		{
			val:       "",
			expectErr: true,
		},
		{
			val:       "blah",
			expectErr: true,
		},
		{
			val:       "1.1.1g",
			expectErr: true,
		},
	} {
		t.Run(tc.val, func(t *testing.T) {
			b, err := bytesize.Parse(tc.val)
			if tc.expectErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if b.Bytes() != float64(tc.expectedBytes) {
				t.Errorf("expected %v bytes, got %v bytes", tc.expectedBytes, b.Bytes())
			}
			if b.String() != tc.expectedString {
				t.Errorf("expected %v, got %v", tc.expectedString, b.String())
			}
			if tc.expectedKilobytes != nil && b.Kilobytes() != *tc.expectedKilobytes {
				t.Errorf("expected %v kilobytes, got %v kilobytes", *tc.expectedKilobytes, b.As(bytesize.Kilobyte))
			}
			if tc.expectedKibibytes != nil && b.Kibibytes() != *tc.expectedKibibytes {
				t.Errorf("expected %v kibibytes, got %v kibibytes", *tc.expectedKibibytes, b.As(bytesize.Kibibyte))
			}
			if tc.expectedMegabytes != nil && b.Megabytes() != *tc.expectedMegabytes {
				t.Errorf("expected %v megabytes, got %v megabytes", *tc.expectedMegabytes, b.As(bytesize.Megabyte))
			}
			if tc.expectedMebibytes != nil && b.Mebibytes() != *tc.expectedMebibytes {
				t.Errorf("expected %v mebibytes, got %v mebibytes", *tc.expectedMebibytes, b.As(bytesize.Mebibyte))
			}
			if tc.expectedGigabytes != nil && b.Gigabytes() != *tc.expectedGigabytes {
				t.Errorf("expected %v gigabytes, got %v gigabytes", *tc.expectedGigabytes, b.As(bytesize.Gigabyte))
			}
			if tc.expectedGibibytes != nil && b.Gibibytes() != *tc.expectedGibibytes {
				t.Errorf("expected %v gibibytes, got %v gibibytes", *tc.expectedGibibytes, b.As(bytesize.Gibibyte))
			}
			if tc.expectedTerabytes != nil && b.Terabytes() != *tc.expectedTerabytes {
				t.Errorf("expected %v terabytes, got %v terabytes", *tc.expectedTerabytes, b.As(bytesize.Terabyte))
			}
			if tc.expectedTebibytes != nil && b.Tebibytes() != *tc.expectedTebibytes {
				t.Errorf("expected %v tebibytes, got %v tebibytes", *tc.expectedTebibytes, b.As(bytesize.Tebibyte))
			}
			if tc.expectedPetabytes != nil && b.Petabytes() != *tc.expectedPetabytes {
				t.Errorf("expected %v petabytes, got %v petabytes", *tc.expectedPetabytes, b.As(bytesize.Petabyte))
			}
			if tc.expectedPebibytes != nil && b.Pebibytes() != *tc.expectedPebibytes {
				t.Errorf("expected %v pebibytes, got %v pebibytes", *tc.expectedPebibytes, b.As(bytesize.Pebibyte))
			}
			if tc.expectedExabytes != nil && b.Exabytes() != *tc.expectedExabytes {
				t.Errorf("expected %v exabytes, got %v exabytes", *tc.expectedExabytes, b.As(bytesize.Exabyte))
			}
			if tc.expectedExbibytes != nil && b.Exbibytes() != *tc.expectedExbibytes {
				t.Errorf("expected %v exbibytes, got %v exbibytes", *tc.expectedExbibytes, b.As(bytesize.Exbibyte))
			}
		})
	}
}
