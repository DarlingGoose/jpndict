package jpndict

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewJiTenDex(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	j, _ := NewJiTenDex(filepath.Join(wd, "tmp"), "", true)
	err = j.Download()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := j.Search(Search{Text: "かぜ"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	print(string(d))
}

type tc struct {
	search       string
	expectedKana string
	hasAudio     bool
}

func TestJiTenDex_Search(t *testing.T) {
	tcs := []tc{
		{
			search:       "最大",
			expectedKana: "さいだい",
			hasAudio:     true,
		},
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	j, _ := NewJiTenDex(filepath.Join(wd, "tmp"), "", true)
	err = j.Download()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tcs {
		t.Run(tc.search, func(t *testing.T) {
			resp, err := j.Search(Search{Text: tc.search, WithAudio: true})
			if err != nil {
				t.Errorf("failed to find word")
				return
			}
			if resp.Entry == nil {
				t.Errorf("expected entry")
				return
			}
			if resp.Entry.Reading != tc.expectedKana {
				t.Errorf("reading missmatch expected:%s got:%s", tc.expectedKana, resp.Entry.Reading)
			}
			if tc.hasAudio && (resp.Entry.Pronunciation == nil || resp.Entry.Pronunciation.Audio == "") {
				t.Errorf("expected audio for %s", tc.search)
			}
			_, err = resp.PlayAudio(true)
			if err != nil {
				t.Errorf("failed to play:%v", err)
				return
			}
		})
	}
}

func TestSegmentDictionaryMatches(t *testing.T) {
	tcs := []struct {
		name    string
		input   string
		keys    map[string]bool
		all     []string
		longest []string
	}{
		{
			name:  "longest non-overlapping matches",
			input: "軍人家系",
			keys: map[string]bool{
				"軍":  true,
				"軍人": true,
				"家":  true,
				"家系": true,
			},
			all:     []string{"軍人", "軍", "家系", "家"},
			longest: []string{"軍人", "家系"},
		},
		{
			name:  "include exact query once",
			input: "最大",
			keys: map[string]bool{
				"最":  true,
				"最大": true,
				"大":  true,
			},
			all:     []string{"最大", "最", "大"},
			longest: []string{"最大"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			all := allDictionaryMatches(tc.input, func(s string) bool {
				return tc.keys[s]
			})
			if !reflect.DeepEqual(all, tc.all) {
				t.Fatalf("unexpected all matches: got=%v want=%v", all, tc.all)
			}

			longest := longestDictionaryMatches(tc.input, func(s string) bool {
				return tc.keys[s]
			})
			if !reflect.DeepEqual(longest, tc.longest) {
				t.Fatalf("unexpected longest matches: got=%v want=%v", longest, tc.longest)
			}
		})
	}
}

func TestSearchAllKeys(t *testing.T) {
	tcs := []struct {
		name    string
		input   string
		keys    map[string]bool
		opts    SearchAllOptions
		want    []string
		wantNil bool
	}{
		{
			name:  "phrase uses exact token matches",
			input: "戦闘 疲労 軽減",
			keys: map[string]bool{
				"戦":  true,
				"戦闘": true,
				"疲":  true,
				"疲労": true,
				"軽":  true,
				"軽減": true,
			},
			want: []string{"戦闘", "疲労", "軽減"},
		},
		{
			name:  "phrase falls back to per-term segmentation",
			input: "軍人 家系",
			keys: map[string]bool{
				"軍":  true,
				"軍人": true,
				"家":  true,
				"家系": true,
			},
			opts: SearchAllOptions{LongestOnly: true},
			want: []string{"軍人", "家系"},
		},
		{
			name:  "phrase requires every term to match",
			input: "軍人 missing",
			keys: map[string]bool{
				"軍人": true,
			},
			wantNil: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			got := searchAllKeys(Search{Text: tc.input}, func(s string) bool {
				return tc.keys[s]
			}, tc.opts)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil matches, got=%v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected phrase matches: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestJiTenDex_SearchAll(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	dict, err := NewJiTenDex(filepath.Join(wd, "tmp"), "", false)
	if err != nil {
		t.Fatal(err)
	}

	j, ok := dict.(*JiTenDex)
	if !ok {
		t.Fatal("expected JiTenDex")
	}

	if err := j.Download(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name             string
		query            string
		wantCount        int
		wantLongestCount int
	}{
		{
			name:             "maximum",
			query:            "最大",
			wantCount:        1,
			wantLongestCount: 1,
		},
		{
			name:             "battle fatigue mitigation phrase",
			query:            "戦闘疲労軽減",
			wantCount:        3,
			wantLongestCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all, err := j.SearchAll(Search{Text: tt.query})
			if err != nil {
				t.Fatal(err)
			}
			if len(all) == 0 {
				t.Fatal("expected search all results")
			}

			longest, err := j.SearchAllWithOptions(Search{Text: tt.query}, SearchAllOptions{LongestOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(longest) == 0 {
				t.Fatal("expected longest-only results")
			}

			if tt.wantCount > 0 && len(all) != tt.wantCount {
				t.Fatalf("expected all count=%d, got=%d headwords=%v",
					tt.wantCount,
					len(all),
					responseHeadwords(all),
				)
			}

			if tt.wantLongestCount > 0 && len(longest) != tt.wantLongestCount {
				t.Fatalf("expected longest-only count=%d, got=%d headwords=%v",
					tt.wantLongestCount,
					len(longest),
					responseHeadwords(longest),
				)
			}
		})
	}
}

func responseHeadwords(responses []*Response) []string {
	headwords := make([]string, 0, len(responses))
	for _, resp := range responses {
		if resp.Entry == nil {
			headwords = append(headwords, "")
			continue
		}
		headwords = append(headwords, resp.Entry.Headword)
	}
	return headwords
}
