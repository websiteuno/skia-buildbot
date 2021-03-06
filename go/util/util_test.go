package util

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"testing"
	"time"

	"go.skia.org/infra/go/testutils"

	assert "github.com/stretchr/testify/require"
)

func TestAtMost(t *testing.T) {
	testutils.SmallTest(t)
	a := AtMost([]string{"a", "b"}, 3)
	if got, want := len(a), 2; got != want {
		t.Errorf("Wrong length: Got %v Want %v", got, want)
	}

	a = AtMost([]string{"a", "b"}, 1)
	if got, want := len(a), 1; got != want {
		t.Errorf("Wrong length: Got %v Want %v", got, want)
	}

	a = AtMost([]string{"a", "b"}, 0)
	if got, want := len(a), 0; got != want {
		t.Errorf("Wrong length: Got %v Want %v", got, want)
	}
}

func TestSSliceEqual(t *testing.T) {
	testutils.SmallTest(t)
	testcases := []struct {
		a    []string
		b    []string
		want bool
	}{
		{
			a:    []string{},
			b:    []string{},
			want: true,
		},
		{
			a:    nil,
			b:    []string{},
			want: false,
		},
		{
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			a:    []string{"foo"},
			b:    []string{},
			want: false,
		},
		{
			a:    []string{"foo", "bar"},
			b:    []string{"bar", "foo"},
			want: true,
		},
	}

	for _, tc := range testcases {
		if got, want := SSliceEqual(tc.a, tc.b), tc.want; got != want {
			t.Errorf("SSliceEqual(%#v, %#v): Got %v Want %v", tc.a, tc.b, got, want)
		}
	}
}

func TestIntersectIntSets(t *testing.T) {
	testutils.SmallTest(t)
	sets := []map[int]bool{
		map[int]bool{1: true, 2: true, 3: true, 4: true},
		map[int]bool{2: true, 4: true, 5: true, 7: true},
	}
	minIdx := 1
	intersect := IntersectIntSets(sets, minIdx)
	assert.Equal(t, map[int]bool{2: true, 4: true}, intersect)
}

func TestAddParamsToParamSet(t *testing.T) {
	testutils.SmallTest(t)
	testCases := []struct {
		a       map[string][]string
		b       map[string]string
		wantFoo []string
	}{
		{
			a: map[string][]string{
				"foo": []string{"a", "b"},
			},
			b: map[string]string{
				"foo": "c",
			},
			wantFoo: []string{"a", "b", "c"},
		},
		{
			a: map[string][]string{
				"foo": []string{},
			},
			b: map[string]string{
				"foo": "c",
			},
			wantFoo: []string{"c"},
		},
		{
			a: map[string][]string{
				"foo": []string{"c"},
			},
			b: map[string]string{
				"foo": "c",
			},
			wantFoo: []string{"c"},
		},
		{
			a: map[string][]string{},
			b: map[string]string{
				"foo": "c",
			},
			wantFoo: []string{"c"},
		},
		{
			a: map[string][]string{
				"foo": []string{"c"},
			},
			b:       map[string]string{},
			wantFoo: []string{"c"},
		},
	}
	for _, tc := range testCases {
		if got, want := AddParamsToParamSet(tc.a, tc.b)["foo"], tc.wantFoo; !SSliceEqual(got, want) {
			t.Errorf("Merge failed: Got %v Want %v", got, want)
		}
	}
}

func TestAddParamSetToParamSet(t *testing.T) {
	testutils.SmallTest(t)
	testCases := []struct {
		a       map[string][]string
		b       map[string][]string
		wantFoo []string
	}{
		{
			a: map[string][]string{
				"foo": []string{"a", "b"},
			},
			b: map[string][]string{
				"foo": []string{"c"},
			},
			wantFoo: []string{"a", "b", "c"},
		},
		{
			a: map[string][]string{
				"foo": []string{},
			},
			b: map[string][]string{
				"foo": []string{"c"},
			},
			wantFoo: []string{"c"},
		},
		{
			a: map[string][]string{
				"foo": []string{"c"},
			},
			b: map[string][]string{
				"foo": []string{},
			},
			wantFoo: []string{"c"},
		},
		{
			a: map[string][]string{
				"foo": []string{"c"},
			},
			b: map[string][]string{
				"bar": []string{"b"},
			},
			wantFoo: []string{"c"},
		},
	}
	for _, tc := range testCases {
		if got, want := AddParamSetToParamSet(tc.a, tc.b)["foo"], tc.wantFoo; !SSliceEqual(got, want) {
			t.Errorf("Merge failed: Got %v Want %v", got, want)
		}
	}
}

func TestAnyMatch(t *testing.T) {
	testutils.SmallTest(t)
	slice := []*regexp.Regexp{
		regexp.MustCompile("somestring"),
		regexp.MustCompile("^abcdefg$"),
		regexp.MustCompile("^defg123"),
		regexp.MustCompile("abc\\.xyz"),
	}
	tc := map[string]bool{
		"somestring":      true,
		"somestringother": true,
		"abcdefg":         true,
		"abcdefgh":        false,
		"defg1234":        true,
		"cdefg123":        false,
		"abc.xyz":         true,
		"abcqxyz":         false,
	}
	for s, e := range tc {
		assert.Equal(t, e, AnyMatch(slice, s))
	}
}

func TestIsNil(t *testing.T) {
	testutils.SmallTest(t)
	assert.True(t, IsNil(nil))
	assert.False(t, IsNil(false))
	assert.False(t, IsNil(0))
	assert.False(t, IsNil(""))
	assert.False(t, IsNil([0]int{}))
	type Empty struct{}
	assert.False(t, IsNil(Empty{}))
	assert.True(t, IsNil(chan interface{}(nil)))
	assert.False(t, IsNil(make(chan interface{})))
	var f func()
	assert.True(t, IsNil(f))
	assert.False(t, IsNil(func() {}))
	assert.True(t, IsNil(map[bool]bool(nil)))
	assert.False(t, IsNil(make(map[bool]bool)))
	assert.True(t, IsNil([]int(nil)))
	assert.False(t, IsNil([][]int{nil}))
	assert.True(t, IsNil((*int)(nil)))
	var i int
	assert.False(t, IsNil(&i))
	var pi *int
	assert.True(t, IsNil(pi))
	assert.True(t, IsNil(&pi))
	var ppi **int
	assert.True(t, IsNil(&ppi))
	var c chan interface{}
	assert.True(t, IsNil(&c))
	var w io.Writer
	assert.True(t, IsNil(w))
	w = (*bytes.Buffer)(nil)
	assert.True(t, IsNil(w))
	w = &bytes.Buffer{}
	assert.False(t, IsNil(w))
	assert.False(t, IsNil(&w))
	var ii interface{}
	ii = &pi
	assert.True(t, IsNil(ii))
}

func TestUnixFloatToTime(t *testing.T) {
	testutils.SmallTest(t)
	cases := []struct {
		in  float64
		out time.Time
	}{
		{
			in:  1414703190.292151927,
			out: time.Unix(1414703190, 292000000),
		},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.out, UnixFloatToTime(tc.in))
	}
}

func TestTimeToUnixFloat(t *testing.T) {
	testutils.SmallTest(t)
	cases := []struct {
		in  time.Time
		out float64
	}{
		{
			in:  time.Unix(1414703190, 292000000),
			out: 1414703190.292000,
		},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.out, TimeToUnixFloat(tc.in))
	}
}

func TestTimeConversion(t *testing.T) {
	testutils.SmallTest(t)
	cases := []float64{
		0.0,
		1.0,
		1414703190.0,
		1414703190.292000,
	}
	for _, tc := range cases {
		assert.Equal(t, tc, TimeToUnixFloat(UnixFloatToTime(tc)))
	}
}

func TestMD5Hash(t *testing.T) {
	testutils.SmallTest(t)
	m_1 := map[string]string{"key1": "val1"}
	m_2 := map[string]string{}
	var m_3 map[string]string = nil
	m_4 := map[string]string{
		"k3": "v1",
		"k2": "v2",
		"k1": "v3",
		"k4": "v4",
	}

	h_1, err := MD5Params(m_1)
	assert.NoError(t, err)

	h_2, err := MD5Params(m_2)
	assert.NoError(t, err)

	h_3, err := MD5Params(m_3)
	assert.NoError(t, err)
	assert.Equal(t, 32, len(h_1))
	assert.Equal(t, 32, len(h_2))
	assert.Equal(t, 32, len(h_3))
	assert.NotEqual(t, h_1, h_2)
	assert.NotEqual(t, h_1, h_3)
	assert.Equal(t, h_2, h_3)

	// Ensure that we get the same hash every time.
	h_4, err := MD5Params(m_4)
	assert.NoError(t, err)
	for i := 0; i < 100; i++ {
		h, err := MD5Params(m_4)
		assert.NoError(t, err)
		assert.Equal(t, h_4, h)
	}
	h, err := MD5Params(map[string]string{
		"k4": "v4",
		"k2": "v2",
		"k3": "v1",
		"k1": "v3",
	})
	assert.NoError(t, err)
	assert.Equal(t, h_4, h)
}

func TestBugsFromCommitMsg(t *testing.T) {
	testutils.SmallTest(t)
	cases := []struct {
		in  string
		out map[string][]string
	}{
		{
			in: "BUG=skia:1234",
			out: map[string][]string{
				"skia": []string{"1234"},
			},
		},
		{
			in: "BUG=skia:1234,skia:4567",
			out: map[string][]string{
				"skia": []string{"1234", "4567"},
			},
		},
		{
			in: "BUG=skia:1234,skia:4567,skia:8901",
			out: map[string][]string{
				"skia": []string{"1234", "4567", "8901"},
			},
		},
		{
			in: "BUG=1234",
			out: map[string][]string{
				"chromium": []string{"1234"},
			},
		},
		{
			in: "BUG=skia:1234, 456",
			out: map[string][]string{
				"chromium": []string{"456"},
				"skia":     []string{"1234"},
			},
		},
		{
			in: `Lorem ipsum dolor sit amet, consectetur adipiscing elit.

Quisque feugiat, mi et tristique dignissim, sapien risus tristique mi, non dignissim nibh erat ut ex.

BUG=1234, skia:5678
`,
			out: map[string][]string{
				"chromium": []string{"1234"},
				"skia":     []string{"5678"},
			},
		},
	}
	for _, tc := range cases {
		result := BugsFromCommitMsg(tc.in)
		assert.Equal(t, tc.out, result)
	}
}

func TestIsDirEmpty(t *testing.T) {
	testutils.SmallTest(t)
	d, err := ioutil.TempDir(os.TempDir(), "test_empty")
	assert.NoError(t, err)
	defer RemoveAll(d)

	// Directory is initially empty.
	empty, err := IsDirEmpty(d)
	assert.NoError(t, err)
	assert.True(t, empty)

	// Add a file in the directory.
	f, err := ioutil.TempFile(d, "test_file")
	assert.NoError(t, err)
	_, err = f.WriteString("testing")
	Close(f)
	assert.NoError(t, err)
	empty, err = IsDirEmpty(d)
	assert.NoError(t, err)
	assert.False(t, empty)

	// Test non existent directory.
	empty, err = IsDirEmpty(path.Join(d, "nonexistent_dir"))
	assert.NotNil(t, err)
}

type DomainTestCase struct {
	DomainA string
	DomainB string
	Match   bool
}

func TestCookieDomainMatch(t *testing.T) {
	testutils.SmallTest(t)
	// Test cases borrowed from test_domain_match in
	// https://svn.python.org/projects/python/trunk/Lib/test/test_cookielib.py
	testCases := []DomainTestCase{
		DomainTestCase{DomainA: "x.y.com", DomainB: "x.Y.com", Match: true},
		DomainTestCase{DomainA: "x.y.com", DomainB: ".Y.com", Match: true},
		DomainTestCase{DomainA: "x.y.com", DomainB: "Y.com", Match: false},
		DomainTestCase{DomainA: "a.b.c.com", DomainB: ".c.com", Match: true},
		DomainTestCase{DomainA: ".c.com", DomainB: "a.b.c.com", Match: false},
		DomainTestCase{DomainA: "example.local", DomainB: ".local", Match: true},
		DomainTestCase{DomainA: "blah.blah", DomainB: "", Match: false},
		DomainTestCase{DomainA: "", DomainB: ".rhubarb.rhubarb", Match: false},
		DomainTestCase{DomainA: "", DomainB: "", Match: true},

		DomainTestCase{DomainA: "acme.com", DomainB: "acme.com", Match: true},
		DomainTestCase{DomainA: "acme.com", DomainB: ".acme.com", Match: false},
		DomainTestCase{DomainA: "rhubarb.acme.com", DomainB: ".acme.com", Match: true},
		DomainTestCase{DomainA: "www.rhubarb.acme.com", DomainB: ".acme.com", Match: true},
		DomainTestCase{DomainA: "y.com", DomainB: "Y.com", Match: true},
		DomainTestCase{DomainA: ".y.com", DomainB: "Y.com", Match: false},
		DomainTestCase{DomainA: ".y.com", DomainB: ".Y.com", Match: true},
		DomainTestCase{DomainA: "x.y.com", DomainB: ".com", Match: true},
		DomainTestCase{DomainA: "x.y.com", DomainB: "com", Match: false},
		DomainTestCase{DomainA: "x.y.com", DomainB: "m", Match: false},
		DomainTestCase{DomainA: "x.y.com", DomainB: ".m", Match: false},
		DomainTestCase{DomainA: "x.y.com", DomainB: "", Match: false},
		DomainTestCase{DomainA: "x.y.com", DomainB: ".", Match: false},
	}
	for _, tc := range testCases {
		assert.Equal(t, tc.Match, CookieDomainMatch(tc.DomainA, tc.DomainB))
	}
}

func TestValidateCommit(t *testing.T) {
	testutils.SmallTest(t)
	tc := map[string]bool{
		"":       false,
		"abc123": false,
		"abcde12345abcde12345abcde12345abcde12345":  true,
		"abcde12345abcde12345abcde12345abcde1234":   false,
		"abcde12345abcde12345abcde12345abcde123456": false,
		"abcde12345abcde12345abcde12345abcde1234g":  false,
		"abcde12345abcde12345abcde12345abcde1234 ":  false,
	}
	for input, expect := range tc {
		assert.Equal(t, ValidateCommit(input), expect)
	}
}

func TestPermute(t *testing.T) {
	testutils.SmallTest(t)

	assert.Equal(t, [][]int{}, Permute([]int{}))
	assert.Equal(t, [][]int{[]int{0}}, Permute([]int{0}))
	assert.Equal(t, [][]int{[]int{0, 1}, []int{1, 0}}, Permute([]int{0, 1}))
	assert.Equal(t, [][]int{
		[]int{0, 1, 2},
		[]int{0, 2, 1},
		[]int{1, 0, 2},
		[]int{1, 2, 0},
		[]int{2, 0, 1},
		[]int{2, 1, 0},
	}, Permute([]int{0, 1, 2}))
	assert.Equal(t, [][]int{
		[]int{0, 1, 2, 3},
		[]int{0, 1, 3, 2},
		[]int{0, 2, 1, 3},
		[]int{0, 2, 3, 1},
		[]int{0, 3, 1, 2},
		[]int{0, 3, 2, 1},
		[]int{1, 0, 2, 3},
		[]int{1, 0, 3, 2},
		[]int{1, 2, 0, 3},
		[]int{1, 2, 3, 0},
		[]int{1, 3, 0, 2},
		[]int{1, 3, 2, 0},
		[]int{2, 0, 1, 3},
		[]int{2, 0, 3, 1},
		[]int{2, 1, 0, 3},
		[]int{2, 1, 3, 0},
		[]int{2, 3, 0, 1},
		[]int{2, 3, 1, 0},
		[]int{3, 0, 1, 2},
		[]int{3, 0, 2, 1},
		[]int{3, 1, 0, 2},
		[]int{3, 1, 2, 0},
		[]int{3, 2, 0, 1},
		[]int{3, 2, 1, 0},
	}, Permute([]int{0, 1, 2, 3}))
}

func TestPermuteStrings(t *testing.T) {
	testutils.SmallTest(t)

	assert.Equal(t, [][]string{}, PermuteStrings([]string{}))
	assert.Equal(t, [][]string{[]string{"a"}}, PermuteStrings([]string{"a"}))
	assert.Equal(t, [][]string{[]string{"a", "b"}, []string{"b", "a"}}, PermuteStrings([]string{"a", "b"}))
	assert.Equal(t, [][]string{
		[]string{"a", "b", "c"},
		[]string{"a", "c", "b"},
		[]string{"b", "a", "c"},
		[]string{"b", "c", "a"},
		[]string{"c", "a", "b"},
		[]string{"c", "b", "a"},
	}, PermuteStrings([]string{"a", "b", "c"}))
	assert.Equal(t, [][]string{
		[]string{"a", "b", "c", "d"},
		[]string{"a", "b", "d", "c"},
		[]string{"a", "c", "b", "d"},
		[]string{"a", "c", "d", "b"},
		[]string{"a", "d", "b", "c"},
		[]string{"a", "d", "c", "b"},
		[]string{"b", "a", "c", "d"},
		[]string{"b", "a", "d", "c"},
		[]string{"b", "c", "a", "d"},
		[]string{"b", "c", "d", "a"},
		[]string{"b", "d", "a", "c"},
		[]string{"b", "d", "c", "a"},
		[]string{"c", "a", "b", "d"},
		[]string{"c", "a", "d", "b"},
		[]string{"c", "b", "a", "d"},
		[]string{"c", "b", "d", "a"},
		[]string{"c", "d", "a", "b"},
		[]string{"c", "d", "b", "a"},
		[]string{"d", "a", "b", "c"},
		[]string{"d", "a", "c", "b"},
		[]string{"d", "b", "a", "c"},
		[]string{"d", "b", "c", "a"},
		[]string{"d", "c", "a", "b"},
		[]string{"d", "c", "b", "a"},
	}, PermuteStrings([]string{"a", "b", "c", "d"}))
}
