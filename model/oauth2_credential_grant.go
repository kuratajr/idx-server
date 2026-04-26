package model

import "time"

type Oauth2GrantGranteeType string

const (
	Oauth2GrantUser Oauth2GrantGranteeType = "user"
	Oauth2GrantRole Oauth2GrantGranteeType = "role"
	Oauth2GrantAll  Oauth2GrantGranteeType = "all"
)

type Oauth2GrantPerm string

const (
	Oauth2PermUse    Oauth2GrantPerm = "use"
	Oauth2PermManage Oauth2GrantPerm = "manage"
)

type Oauth2CredentialGrant struct {
	ID uint64 `gorm:"primaryKey" json:"id"`

	CredentialID uint64 `gorm:"not null;index;uniqueIndex:uidx_cred_grant" json:"credential_id"`

	GranteeType Oauth2GrantGranteeType `gorm:"size:16;not null;index;uniqueIndex:uidx_cred_grant" json:"grantee_type"`
	GranteeID   uint64                 `gorm:"not null;index;uniqueIndex:uidx_cred_grant" json:"grantee_id"`
	Perm        Oauth2GrantPerm        `gorm:"size:16;not null;index;uniqueIndex:uidx_cred_grant" json:"perm"`

	CreatedByUserID uint64    `gorm:"index;not null" json:"created_by_user_id"`
	CreatedAt       time.Time `gorm:"index" json:"created_at"`
}

