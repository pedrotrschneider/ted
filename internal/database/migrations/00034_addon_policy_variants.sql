-- +goose Up
-- Split manifest-ID policies by public variant name, preserving prior switches
-- and revisions for every known variant and the retained saved name. Neither
-- configured URLs nor credentials participate in the shared policy identity.
CREATE TEMP TABLE addon_policy_variants ON COMMIT DROP AS
 SELECT encode(sha256(convert_to(p.id,'UTF8') || decode('00','hex') || convert_to(names.name,'UTF8')),'hex') AS id,
 names.name,p.enabled,p.revision
 FROM stremio_addon_policies p
 CROSS JOIN LATERAL (
   SELECT p.name
   UNION
   SELECT regexp_replace(d->'manifest'->>'name','(?i)(https?://|stremio://)\S+','[link]','g')
   FROM stremio_addon_collections c CROSS JOIN LATERAL jsonb_array_elements(c.payload) d
   WHERE encode(sha256(convert_to(d->'manifest'->>'id','UTF8')),'hex')=p.id
 ) names;
DELETE FROM stremio_addon_policies;
INSERT INTO stremio_addon_policies(id,name,enabled,revision)
 SELECT id,name,bool_and(enabled),max(revision) FROM addon_policy_variants GROUP BY id,name;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Add-on switches are durable; restore a backup instead of merging them'; END $$;
-- +goose StatementEnd
