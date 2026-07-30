CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE movies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    canonical_title text NOT NULL,
    release_year integer,
    external_ids jsonb NOT NULL DEFAULT '{}'::jsonb,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX movies_title_search_idx ON movies USING gin (to_tsvector('simple', canonical_title));

CREATE TABLE venues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    address jsonb NOT NULL,
    latitude double precision NOT NULL,
    longitude double precision NOT NULL,
    timezone text NOT NULL,
    external_ids jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX venues_geo_idx ON venues (latitude, longitude);

CREATE TABLE auditoria (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id uuid NOT NULL REFERENCES venues(id),
    name text NOT NULL,
    reserved_seating boolean NOT NULL DEFAULT false,
    layout_payload jsonb,
    layout_confidence text,
    layout_observed_at timestamptz,
    UNIQUE (venue_id, name)
);

CREATE TABLE showtimes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    movie_id uuid NOT NULL REFERENCES movies(id),
    venue_id uuid NOT NULL REFERENCES venues(id),
    auditorium_id uuid REFERENCES auditoria(id),
    starts_at timestamptz NOT NULL,
    presentation_format text NOT NULL,
    language text,
    accessibility jsonb NOT NULL DEFAULT '{}'::jsonb,
    amenities jsonb NOT NULL DEFAULT '[]'::jsonb,
    booking_url text,
    provider_name text NOT NULL,
    provider_showtime_id text NOT NULL,
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    UNIQUE (provider_name, provider_showtime_id)
);

CREATE INDEX showtimes_discovery_idx ON showtimes (movie_id, starts_at);
CREATE INDEX showtimes_venue_time_idx ON showtimes (venue_id, starts_at);

CREATE TABLE source_observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider_name text NOT NULL,
    entity_kind text NOT NULL,
    provider_entity_id text NOT NULL,
    payload_hash text NOT NULL,
    observed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    status text NOT NULL,
    UNIQUE (provider_name, entity_kind, provider_entity_id, observed_at)
);

CREATE INDEX source_observations_expiry_idx ON source_observations (expires_at);

CREATE TABLE seat_queries (
    id text PRIMARY KEY,
    idempotency_key text UNIQUE,
    request_hash text NOT NULL,
    request jsonb NOT NULL,
    response jsonb NOT NULL,
    status text NOT NULL,
    generated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX seat_queries_expiry_idx ON seat_queries (expires_at);
