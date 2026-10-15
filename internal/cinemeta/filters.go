package cinemeta

import "time"

type Filters struct {
	Catalog string
	Genre   string
	Year    int
}

var genres = map[string]bool{"": true, "Action": true, "Adventure": true, "Animation": true, "Biography": true, "Comedy": true, "Crime": true, "Documentary": true, "Drama": true, "Family": true, "Fantasy": true, "History": true, "Horror": true, "Mystery": true, "Romance": true, "Sci-Fi": true, "Sport": true, "Thriller": true, "War": true, "Western": true}

func ValidGenre(genre string) bool { return genres[genre] }
func (f Filters) catalogID(kind, search string) (string, error) {
	if !ValidGenre(f.Genre) {
		return "", ErrInvalid
	}
	if search != "" && (f.Genre != "" || f.Year != 0 || (f.Catalog != "" && f.Catalog != "popular")) {
		return "", ErrInvalid
	}
	switch f.Catalog {
	case "", "popular":
		if f.Year == 0 {
			return "top", nil
		}
	case "featured":
		if f.Year == 0 {
			return "imdbRating", nil
		}
	case "new":
		min := 1920
		if kind == "series" {
			min = 1960
		}
		if f.Genre == "" && f.Year >= min && f.Year <= time.Now().UTC().Year() {
			return "year", nil
		}
	}
	return "", ErrInvalid
}
