package upload

import (
	"testing"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

func TestInheritAdmissionCannotReplaceAccountDeletionLock(t *testing.T) {
	env := newInheritTestEnv(t)
	key := inheritActiveKeyPrefix + testOwner
	lockValue := harukiRedis.AccountDeletionLockPrefix + "test"
	env.redis.Set(key, lockValue)
	if job, problem := env.svc.claimActive(t.Context(), testOwner, "new-job"); problem != errJobAlreadyRunning || job != "" {
		t.Fatalf("job=%q problem=%v", job, problem)
	}
	if value, _ := env.redis.Get(key); value != lockValue {
		t.Fatal("deletion lock replaced")
	}
}
