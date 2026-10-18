package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"entertainmentdistrict/internal/accounts"
	"entertainmentdistrict/internal/watchstate"
)

func (a *API) nativeSeriesTitleWatch(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	a.nativeSeriesWatch(w, r, g, nil)
}

func (a *API) nativeSeasonWatch(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant) {
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil || season < 0 || season > 9999 {
		a.nativeWatchFailure(w, r, watchstate.ErrInvalid)
		return
	}
	a.nativeSeriesWatch(w, r, g, &season)
}

func (a *API) nativeSeriesWatch(w http.ResponseWriter, r *http.Request, g accounts.NativeGrant, season *int) {
	id := r.PathValue("id")
	_, _, err := a.loadNativeSeries(r.Context(), id, false)
	if err != nil {
		a.catalogFailure(w, r, err)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Version string `json:"version"`
			Watched *bool  `json:"watched"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Watched == nil {
			a.nativeWatchFailure(w, r, watchstate.ErrInvalid)
			return
		}
		if season == nil {
			err = a.watch.EditNativeSeries(r.Context(), a.NativeAuth, g, id, input.Version, *input.Watched)
		} else {
			err = a.watch.EditNativeSeason(r.Context(), a.NativeAuth, g, id, *season, input.Version, *input.Watched)
		}
		if err != nil {
			a.nativeWatchFailure(w, r, err)
			return
		}
	}
	var state watchstate.NativeSeriesWatch
	if season == nil {
		state, err = a.watch.NativeSeriesWatch(r.Context(), g.User.ID, id)
	} else {
		state, err = a.watch.NativeSeasonWatch(r.Context(), g.User.ID, id, *season)
	}
	if err != nil {
		a.nativeWatchFailure(w, r, err)
		return
	}
	counts := map[string]*nativeEpisodeCounts{}
	today := time.Now().In(a.CalendarLocation).Format(time.DateOnly)
	for _, item := range state.Items {
		parts := strings.Split(item.Media.ID, ":")
		released := state.ReleaseDates[item.Media.ID]
		if len(parts) != 3 || released == "" || released > today {
			continue
		}
		count := counts[parts[1]]
		if count == nil {
			count = &nativeEpisodeCounts{}
			counts[parts[1]] = count
		}
		count.Total++
		if item.Played {
			count.Watched++
		}
	}
	joint, err := a.jointSeasonWatches(r.Context(), g.User.ID, id, season)
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	other, err := a.watch.OtherSeasonIndicators(r.Context(), g.User.ID, id, season, today)
	if err != nil {
		a.nativeFail(w, r, err)
		return
	}
	a.respondNativeCatalog(w, r, struct {
		watchstate.NativeSeriesWatch
		UserCounts   map[string][]watchstate.UserIndicator `json:"user_counts,omitempty"`
		JointCounts  map[string][]jointWatch               `json:"joint_counts"`
		SeasonCounts map[string]*nativeEpisodeCounts       `json:"season_counts"`
	}{state, other, joint, counts})
}
