-- Per-user asset inventory for the pod archive exporter.
--
-- This is the ONLY place that knows Immich's schema. It asserts the table and
-- every column it depends on before selecting anything, so an Immich upgrade
-- that renames one fails the job loudly instead of exporting nothing. That is
-- not hypothetical: Immich moved to Kysely and the table is now `asset`,
-- singular, while its columns stayed camelCase and therefore need quoting.
--
-- Verify after an Immich upgrade with:
--   kubectl exec -n immich database-1 -- psql -U postgres -d app -At \
--     -c "select column_name from information_schema.columns
--         where table_schema='public' and table_name='asset' order by 1;"

\set ON_ERROR_STOP on

DO $$
DECLARE
    missing text;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'asset'
    ) THEN
        RAISE EXCEPTION
            'immich schema drift: table public.asset does not exist. The pod exporter must be updated before it can run again.';
    END IF;

    SELECT string_agg(needed, ', ')
      INTO missing
      FROM unnest(ARRAY[
              'id', 'ownerId', 'originalPath', 'originalFileName', 'type',
              'fileCreatedAt', 'status', 'visibility', 'isExternal', 'isOffline'
           ]) AS needed
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns c
         WHERE c.table_schema = 'public'
           AND c.table_name = 'asset'
           AND c.column_name = needed
     );

    IF missing IS NOT NULL THEN
        RAISE EXCEPTION
            'immich schema drift: asset is missing column(s) %. The pod exporter must be updated before it can run again.',
            missing;
    END IF;
END $$;

-- One compact JSON document per asset.
--
--   status = 'active'   excludes trashed and deleted assets. Anything already
--                       in a pod stays there — append-only — but a trashed
--                       asset is not newly offered.
--   isExternal = false  external libraries live outside the media root and are
--                       not Immich's to copy.
--   isOffline = false   the row outlived its file.
SELECT json_build_object(
    'id',            a."id",
    'owner_id',      a."ownerId",
    'original_path', a."originalPath",
    'file_name',     a."originalFileName",
    'type',          a."type",
    'visibility',    a."visibility"::text,
    'created_at',    to_char(a."fileCreatedAt" AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
)::text
FROM asset a
WHERE a."status" = 'active'
  AND a."isExternal" = false
  AND a."isOffline" = false
ORDER BY a."ownerId", a."fileCreatedAt";
