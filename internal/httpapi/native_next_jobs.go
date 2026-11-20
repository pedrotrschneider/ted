package httpapi

import "context"

func (a *API) StartNextPreparations(ctx context.Context) error {
	return a.addons.StartNextPreparations(ctx)
}
func (a *API) StopNextPreparations(ctx context.Context) error {
	return a.addons.StopNextPreparations(ctx)
}
