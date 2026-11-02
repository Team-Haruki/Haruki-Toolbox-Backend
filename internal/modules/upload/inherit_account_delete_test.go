package upload

import (
	"net/http"
	"testing"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

func TestInheritAdmissionRechecksDeletedAccount(t *testing.T) {
	env := newInheritTestEnv(t)
	env.db.User.DeleteOneID(testOwner).ExecX(t.Context())
	response := env.call(testOwner, http.MethodPost, "/api/inherit/jobs", validJobRequest())
	if response.Status != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Status)
	}
	if env.games != 0 || env.redis.Exists(inheritActiveKeyPrefix+testOwner) {
		t.Fatal("transfer started after account deletion or retained its lock")
	}
}

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
