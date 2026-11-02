package redis

// InheritActiveKeyPrefix is shared by transfer-job admission and permanent
// account deletion so an account cannot disappear while a transfer is running.
const InheritActiveKeyPrefix = "inherit:jobs:active:"

// AccountDeletionLockPrefix cannot collide with the UUIDs used as job IDs.
const AccountDeletionLockPrefix = "account-deletion:"
