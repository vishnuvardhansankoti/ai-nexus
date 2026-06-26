package episodic

import "context"

type contextKey int

const episodeIDKey contextKey = iota

// WithEpisodeID returns a copy of ctx with the episode ID attached.
func WithEpisodeID(ctx context.Context, episodeID string) context.Context {
	return context.WithValue(ctx, episodeIDKey, episodeID)
}

// EpisodeIDFromContext extracts the episode ID stored by WithEpisodeID.
// Returns ("", false) if no ID is present.
func EpisodeIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(episodeIDKey).(string)
	return id, ok && id != ""
}
