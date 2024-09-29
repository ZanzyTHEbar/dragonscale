package global

import "context"

func GetUserIDFromCtx(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(LocalUserKey).(string)
	return userID, ok
}
