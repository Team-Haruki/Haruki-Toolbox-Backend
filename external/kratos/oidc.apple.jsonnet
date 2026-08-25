local claims = {
  email_verified: false,
} + std.extVar('claims');

local verified_email =
  if std.objectHas(claims, 'email') &&
     claims.email != null &&
     claims.email != '' &&
     claims.email_verified == true
  then claims.email
  else null;

{
  identity: {
    traits: {
      // The identity schema requires email. Reject an unverified/missing Apple
      // email instead of linking or provisioning an identity with unsafe data.
      [if verified_email != null then 'email' else null]: verified_email,
    },
    metadata_public: {
      email_verified: verified_email != null,
      social_provider: 'apple',
    },
  },
}
