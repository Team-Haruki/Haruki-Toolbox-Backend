local claims = std.extVar('claims');

local email = if std.objectHas(claims, 'email') then claims.email else '';

local display_name =
  if std.objectHas(claims, 'name') && claims.name != null && claims.name != '' then claims.name
  else
    (if std.objectHas(claims, 'given_name') && claims.given_name != null then claims.given_name else '') +
    (if std.objectHas(claims, 'given_name') && std.objectHas(claims, 'family_name') && claims.given_name != null && claims.family_name != null && claims.given_name != '' && claims.family_name != '' then ' ' else '') +
    (if std.objectHas(claims, 'family_name') && claims.family_name != null then claims.family_name else '');

{
  identity: {
    traits: {
      email: email,
      name: if display_name != '' then display_name else email,
    },
    metadata_public: {
      email_verified: if std.objectHas(claims, 'email_verified') then claims.email_verified else false,
      social_provider: if std.objectHas(claims, 'iss') then claims.iss else 'oidc',
    },
  },
}
