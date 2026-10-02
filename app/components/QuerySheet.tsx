"use client";

import { useEffect, useId, useRef, useState, type Dispatch, type PointerEvent as ReactPointerEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type SetStateAction } from "react";
import { flushSync } from "react-dom";
import {
  createDefaultQuery,
  dateInTimeZone,
  isValidTimeZone,
  type MovieSuggestion,
  type NormalizedSeatZone,
  type ProviderStatus,
  type QueryState,
  type SeatProfile,
  type TimeMode,
} from "../lib/api";
import { AlertIcon, ArrowRightIcon, CalendarIcon, ChevronRightIcon, FilmIcon, LocateIcon, MinusIcon, PinIcon, PlusIcon, RefreshIcon, SearchIcon, SeatIcon, SlidersIcon, WheelchairIcon } from "./icons";
import { SheetClose } from "./Sheet";

export type ProviderState = "checking" | "ready" | "discovery" | "unconfigured" | "unavailable";
export type Problem = { title: string; detail: string; status?: number };
export type LocationMode = NonNullable<ProviderStatus["location_mode"]>;
type Field = "movie" | "location" | "formats" | "timezone";

const formatOptions = ["Standard", "Dolby", "IMAX", "XD", "ScreenX", "3D"];
const timeModes: { value: TimeMode; label: string }[] = [
  { value: "any", label: "Any" },
  { value: "inside", label: "Between" },
  { value: "outside", label: "Outside" },
  { value: "before", label: "Before" },
  { value: "after", label: "After" },
];
const captionModes = [
  { value: "any", label: "Any" },
  { value: "open", label: "Open" },
  { value: "closed", label: "Closed" },
  { value: "none", label: "None" },
];
/** Unit-square zones (x right, y away from the screen) drawn in each preference tile. */
const profiles: { value: SeatProfile; label: string; hint: string; zones: [number, number, number, number][] }[] = [
  { value: "dead_center", label: "Center", hint: "Exact geometric middle", zones: [[.4, .45, .2, .2]] },
  { value: "balanced", label: "Best", hint: "Center and ideal depth", zones: [[.28, .42, .44, .34]] },
  { value: "two_thirds_back", label: "⅔ back", hint: "Classic viewing zone", zones: [[.12, .6, .76, .16]] },
  { value: "aisle", label: "Aisle", hint: "Easy in and out", zones: [[.22, .2, .1, .7], [.68, .2, .1, .7]] },
  { value: "front", label: "Front", hint: "Immersive front section", zones: [[.12, .18, .76, .2]] },
  { value: "back", label: "Back", hint: "More distance", zones: [[.12, .72, .76, .2]] },
  { value: "custom", label: "Custom", hint: "Draw a preferred area", zones: [] },
];
/** Two full weeks, laid out as a 7×2 grid whose columns start on today's weekday. */
const stripDays = 14;
const maxTickets = 8;
const noon = (day: string) => new Date(`${day}T12:00:00Z`);
const dayLabel = new Intl.DateTimeFormat("en-US", { weekday: "long", month: "long", day: "numeric", timeZone: "UTC" });
const weekdayLetter = new Intl.DateTimeFormat("en-US", { weekday: "narrow", timeZone: "UTC" });
const rangeLabel = new Intl.DateTimeFormat("en-US", { weekday: "short", month: "short", day: "numeric", timeZone: "UTC" });
const postalCodePattern = /^\s*\d{5}(?:-\d{4})?\s*$/;
const yearSuffix = /\s*\((?:19|20)\d{2}\)\s*$/;
const defaultCustomZone: NormalizedSeatZone = { minimumX: .28, maximumX: .72, minimumY: .42, maximumY: .76 };
const timezoneOptions: string[] = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
const locationHint: Record<LocationMode, string> = {
  postal_or_coordinates: "Enter a 5-digit ZIP, or tap locate.",
  text_or_coordinates: "Enter a place, or tap locate.",
  coordinates: "Tap locate to use your position.",
};

/** Title identity for matching a poster to a provider title: case, punctuation and a trailing "(YYYY)" are ignored. */
const titleKey = (value: string) => value.replace(yearSuffix, "").toLocaleLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");

export function locationIsReady(state: Pick<QueryState, "location" | "latitude" | "longitude">, mode: LocationMode) {
  if (state.latitude !== undefined && state.longitude !== undefined) return true;
  if (mode === "text_or_coordinates") return Boolean(state.location.trim());
  return mode === "postal_or_coordinates" && postalCodePattern.test(state.location);
}

/** Count of filters that differ from defaults, for the Filters badge. */
function activeFilterCount(draft: QueryState) {
  // Defaults don't depend on the zone; a half-typed zone must not throw in Intl.
  const base = createDefaultQuery();
  return [
    draft.timeMode !== base.timeMode,
    draft.formats.length !== base.formats.length || draft.formats.some((format) => !base.formats.includes(format)),
    draft.maxDistance !== base.maxDistance,
    draft.limitPrice,
    draft.captions !== base.captions,
    draft.audioDescription,
    draft.wheelchairSpaces > 0,
    draft.companionSeats > 0,
    draft.recliners,
    draft.excludeFirstRows !== base.excludeFirstRows,
    draft.allowSplit,
  ].filter(Boolean).length;
}

function zoneAbbreviation(timeZone: string) {
  if (!isValidTimeZone(timeZone)) return "TZ?";
  return new Intl.DateTimeFormat("en-US", { timeZone, timeZoneName: "short" }).formatToParts(new Date()).find((part) => part.type === "timeZoneName")?.value ?? timeZone;
}

export function QuerySheet({
  draft, setDraft, providerState, locationMode, locating, locationProblem, onLocate, onLocationEdit,
  searching, problem, shareErrors, poster, resolveToken, onSubmit,
}: {
  draft: QueryState;
  setDraft: Dispatch<SetStateAction<QueryState>>;
  providerState: ProviderState;
  locationMode: LocationMode;
  locating: boolean;
  locationProblem: string;
  onLocate: () => void;
  onLocationEdit: () => void;
  searching: boolean;
  problem: Problem | null;
  shareErrors: string[];
  poster?: { title: string; url: string; year?: string };
  /** Changes whenever a poster pre-fills the title, asking the field to bind a provider title. */
  resolveToken: number;
  onSubmit: () => void;
}) {
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [shaking, setShaking] = useState("");
  // Errors appear only after a failed submit, then track the draft live until it validates.
  const [checked, setChecked] = useState(false);
  const errorID = useId();
  const locationProblemID = useId();
  const movieInput = useRef<HTMLInputElement>(null);
  const locationInput = useRef<HTMLInputElement>(null);
  const timezoneInput = useRef<HTMLInputElement>(null);
  const formatsGroup = useRef<HTMLFieldSetElement>(null);
  const update = <K extends keyof QueryState>(key: K, value: QueryState[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const filterCount = activeFilterCount(draft);
  const hasCoordinates = draft.latitude !== undefined && draft.longitude !== undefined;
  const sourceReady = providerState === "ready" || providerState === "discovery";
  const zone = zoneAbbreviation(draft.timezone);
  const showPoster = poster && titleKey(poster.title) === titleKey(draft.movie);
  const failing: Field | null = !draft.movie.trim() ? "movie"
    : !locationIsReady(draft, locationMode) ? "location"
      : draft.formats.length === 0 ? "formats"
        : !isValidTimeZone(draft.timezone) ? "timezone"
          : null;
  const error = checked ? failing : null;
  const fieldErrors: Record<Field, string> = { movie: "Pick a movie.", location: locationHint[locationMode], formats: "Pick at least one format.", timezone: "Unknown timezone." };
  const errorNode = error ? <p className="field-error" id={errorID} role="alert">{fieldErrors[error]}</p> : null;
  const openFiltersAndFocus = (target: () => HTMLElement | null | undefined) => {
    // Commit the disclosure first: focus cannot land inside an inert, collapsed panel.
    flushSync(() => setFiltersOpen(true));
    target()?.focus();
  };

  useEffect(() => {
    if (!shaking) return;
    const timer = window.setTimeout(() => setShaking(""), 420);
    return () => window.clearTimeout(timer);
  }, [shaking]);

  const submit = () => {
    if (!failing) {
      setChecked(false);
      onSubmit();
      return;
    }
    setChecked(true);
    setShaking(failing);
    if (failing === "movie") movieInput.current?.focus();
    else if (failing === "location") locationInput.current?.focus();
    else if (failing === "formats") openFiltersAndFocus(() => formatsGroup.current?.querySelector("input"));
    else openFiltersAndFocus(() => timezoneInput.current);
  };

  return (
    <form
      aria-busy={searching}
      className={`query-form${searching ? " is-searching" : ""}`}
      noValidate
      onSubmit={(event) => { event.preventDefault(); submit(); }}
    >
      <header className={`sheet-head query-head${shaking === "movie" ? " shake" : ""}`}>
        <span aria-hidden="true" className="slot">
          {showPoster ? (
            // eslint-disable-next-line @next/next/no-img-element -- provider poster hosts are dynamic.
            <img alt="" className="query-poster" height="54" src={poster.url} style={{ viewTransitionName: "active-poster" }} width="36" />
          ) : <SearchIcon />}
        </span>
        <MovieField
          errorID={error === "movie" ? errorID : undefined}
          enabled={providerState === "ready"}
          inputRef={movieInput}
          movie={draft.movie}
          movieId={draft.movieId}
          onChange={(movie, movieId) => setDraft((current) => ({ ...current, movie, movieId }))}
          resolveToken={resolveToken}
          year={showPoster ? poster.year : undefined}
        />
        <SheetClose />
      </header>

      <div className="sheet-body">
        {error === "movie" ? errorNode : null}
        {providerState === "discovery" ? <p className="notice"><FilmIcon size={16} />Showtimes only — this source has no seat maps.</p> : null}
        {providerState === "unconfigured" || providerState === "unavailable" ? <p className="notice is-error" role="alert"><AlertIcon size={16} />Live source unavailable.</p> : null}

        <div className={`field${shaking === "location" ? " shake" : ""}`}>
          <span aria-hidden="true" className="slot"><PinIcon /></span>
          <input
            aria-describedby={locationProblem ? locationProblemID : error === "location" ? errorID : undefined}
            aria-invalid={Boolean(locationProblem) || error === "location"}
            aria-label="Location"
            autoComplete="postal-code"
            className={hasCoordinates ? "is-located" : ""}
            inputMode={locationMode === "postal_or_coordinates" ? "numeric" : undefined}
            onChange={(event) => {
              const value = event.target.value;
              setDraft((current) => ({ ...current, location: value, latitude: undefined, longitude: undefined }));
              onLocationEdit();
            }}
            placeholder={locationMode === "text_or_coordinates" ? "ZIP, city, or address" : locationMode === "postal_or_coordinates" ? "ZIP" : "Location"}
            ref={locationInput}
            value={draft.location}
          />
          <button aria-label="Use my location" aria-pressed={hasCoordinates} className={`icon-button locate${locating ? " is-busy" : ""}`} onClick={onLocate} type="button"><LocateIcon /></button>
        </div>
        {locationProblem ? <p className="field-error" id={locationProblemID} role="alert">{locationProblem}</p> : error === "location" ? errorNode : null}

        <DateRange
          end={draft.dateEnd}
          onChange={(dateStart, dateEnd) => setDraft((current) => ({ ...current, dateStart, dateEnd }))}
          onZone={() => openFiltersAndFocus(() => timezoneInput.current)}
          start={draft.dateStart}
          timeZone={isValidTimeZone(draft.timezone) ? draft.timezone : "UTC"}
          zone={zone}
        />

        <SeatCount onChange={(value) => update("tickets", value)} value={draft.tickets} />

        <ProfilePicker onChange={(profile) => update("profile", profile)} value={draft.profile} />
        <Reveal open={draft.profile === "custom"}>
          <CustomSeatZonePicker onChange={(zone) => update("customSeatZone", zone)} value={draft.customSeatZone} />
        </Reveal>

        <button aria-expanded={filtersOpen} className="field filters-toggle" onClick={() => setFiltersOpen((open) => !open)} type="button">
          <span aria-hidden="true" className="slot"><SlidersIcon /></span>
          <span>Filters</span>
          {filterCount ? <b className="badge">{filterCount}</b> : null}
          <span aria-hidden="true" className="slot chevron"><ChevronRightIcon /></span>
        </button>
        <Reveal open={filtersOpen}>
          <div className="filters">
            <FilterGroup label="Time">
              <Segmented label="Time window" name="time-mode" onChange={(value) => update("timeMode", value as TimeMode)} options={timeModes} value={draft.timeMode} />
              {draft.timeMode !== "any" ? (
                <div className="time-inputs">
                  {draft.timeMode !== "before" ? <input aria-label={draft.timeMode === "after" ? "After" : "From"} onChange={(event) => update("startTime", event.target.value)} type="time" value={draft.startTime} /> : null}
                  {draft.timeMode === "inside" || draft.timeMode === "outside" ? <span aria-hidden="true">–</span> : null}
                  {draft.timeMode !== "after" ? <input aria-label={draft.timeMode === "before" ? "Before" : "To"} onChange={(event) => update("endTime", event.target.value)} type="time" value={draft.endTime} /> : null}
                </div>
              ) : null}
            </FilterGroup>

            <fieldset aria-describedby={error === "formats" ? errorID : undefined} className={`filter-group${shaking === "formats" ? " shake" : ""}`} ref={formatsGroup}>
              <legend>Format</legend>
              <div className="chips">
                {formatOptions.map((format) => (
                  <label className="chip" key={format}>
                    <input
                      checked={draft.formats.includes(format)}
                      onChange={() => setDraft((current) => ({ ...current, formats: current.formats.includes(format) ? current.formats.filter((item) => item !== format) : [...current.formats, format] }))}
                      type="checkbox"
                    />
                    <span>{format}</span>
                  </label>
                ))}
              </div>
              {error === "formats" ? errorNode : null}
            </fieldset>

            <FilterGroup label="Distance">
              <div className="list">
                <Range label="Distance" max={49} min={2} onChange={(value) => update("maxDistance", value)} suffix=" mi" value={draft.maxDistance} />
              </div>
            </FilterGroup>

            <FilterGroup label="Price">
              <div className="list">
                <Switch checked={draft.limitPrice} label="Limit total" onChange={(value) => update("limitPrice", value)} />
                {draft.limitPrice ? <Range label="Maximum total" max={200} min={10} onChange={(value) => update("maxPrice", value)} prefix="$" step={5} value={draft.maxPrice} /> : null}
              </div>
            </FilterGroup>

            <FilterGroup label="Seats">
              <div className="list">
                <Switch checked={draft.recliners} label="Recliners" onChange={(value) => update("recliners", value)} />
                <Switch checked={draft.allowSplit} label="Split party" onChange={(value) => update("allowSplit", value)} />
                <Stepper label="Skip front rows" max={4} min={0} onChange={(value) => update("excludeFirstRows", value)} value={draft.excludeFirstRows} />
              </div>
            </FilterGroup>

            <FilterGroup icon={<WheelchairIcon size={16} />} label="Access">
              <div className="list">
                <Stepper label="Wheelchair spaces" max={4} min={0} onChange={(value) => update("wheelchairSpaces", value)} value={draft.wheelchairSpaces} />
                <Stepper label="Companion seats" max={4} min={0} onChange={(value) => update("companionSeats", value)} value={draft.companionSeats} />
                <Switch checked={draft.audioDescription} label="Audio description" onChange={(value) => update("audioDescription", value)} />
              </div>
            </FilterGroup>

            <FilterGroup label="Captions">
              <Segmented label="Captions" name="captions" onChange={(value) => update("captions", value)} options={captionModes} value={draft.captions} />
            </FilterGroup>

            <FilterGroup label="Timezone">
              <input
                aria-describedby={error === "timezone" ? errorID : undefined}
                aria-invalid={!isValidTimeZone(draft.timezone)}
                aria-label="Timezone"
                className={`text-input${shaking === "timezone" ? " shake" : ""}`}
                list="centerseat-timezones"
                onChange={(event) => update("timezone", event.target.value)}
                ref={timezoneInput}
                value={draft.timezone}
              />
              <datalist id="centerseat-timezones">{timezoneOptions.map((timezone) => <option key={timezone} value={timezone} />)}</datalist>
              {error === "timezone" ? errorNode : null}
            </FilterGroup>
          </div>
        </Reveal>

        {shareErrors.length ? <p className="notice is-warning" role="alert"><AlertIcon size={16} />Ignored from link: {shareErrors.join(" · ")}</p> : null}
        {problem ? <p className="notice is-error" role="alert"><AlertIcon size={16} /><b>{problem.title}</b> {problem.detail}</p> : null}
      </div>

      <footer className="sheet-foot">
        <button className="search-button" disabled={searching || !sourceReady} type="submit">
          <span>{providerState === "discovery" ? "Find showtimes" : "Search"}</span>
          <ArrowRightIcon />
        </button>
      </footer>

      {searching ? <Scanner /> : null}
    </form>
  );
}

/** Animated auditorium sweep shown while live seat maps are checked. */
function Scanner() {
  return (
    <div className="scanner" role="status">
      <div className="scanner-inner">
        <div aria-hidden="true" className="scanner-grid">
          {Array.from({ length: 6 * 11 }, (_, index) => <i key={index} style={{ "--row": Math.floor(index / 11), "--col": index % 11 } as React.CSSProperties} />)}
        </div>
        <span>Searching</span>
      </div>
    </div>
  );
}

function MovieField({ movie, movieId, onChange, enabled, resolveToken, year, errorID, inputRef }: {
  movie: string;
  movieId?: string;
  onChange: (movie: string, movieId?: string) => void;
  enabled: boolean;
  resolveToken: number;
  /** Release year shown with the poster; breaks ties between same-titled films. */
  year?: string;
  inputRef: React.RefObject<HTMLInputElement | null>;
  /** Set while the field is the failing one; links the input to its error. */
  errorID?: string;
}) {
  const [suggestions, setSuggestions] = useState<MovieSuggestion[]>([]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const listID = useId();
  // True while the title came from a poster: a unique provider match binds silently, otherwise choices open.
  const fromPoster = useRef(false);

  useEffect(() => {
    fromPoster.current = resolveToken > 0;
  }, [resolveToken]);

  useEffect(() => {
    const query = movie.trim();
    if (!enabled || movieId || query.length < 2) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      fetch(`/api/movie-suggestions?q=${encodeURIComponent(query)}`, { signal: controller.signal })
        .then(async (response) => response.ok ? response.json() as Promise<{ suggestions?: MovieSuggestion[] }> : { suggestions: [] })
        .then((payload) => {
          const found = payload.suggestions ?? [];
          if (fromPoster.current) {
            fromPoster.current = false;
            const exact = found.filter((suggestion) => titleKey(suggestion.title) === titleKey(query));
            const matches = exact.length > 1 && year ? exact.filter((suggestion) => suggestion.year === year) : exact;
            if (matches.length === 1) {
              onChange(matches[0].title, matches[0].id);
              setSuggestions([]);
              setOpen(false);
              return;
            }
          }
          setSuggestions(found);
          setOpen(found.length > 0 && document.activeElement === inputRef.current);
          setActive(-1);
        })
        .catch(() => undefined);
    }, fromPoster.current ? 0 : 200);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
    // onChange is recreated by the parent each render; the request depends only on the title state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [movie, movieId, enabled]);

  const choose = (suggestion: MovieSuggestion) => {
    onChange(suggestion.title, suggestion.id);
    setSuggestions([]);
    setOpen(false);
    setActive(-1);
  };

  return (
    <div className="movie-field">
      <input
        aria-activedescendant={open && active >= 0 ? `${listID}-${active}` : undefined}
        aria-autocomplete="list"
        aria-controls={listID}
        aria-expanded={open}
        aria-describedby={errorID}
        aria-invalid={Boolean(errorID)}
        aria-label="Movie"
        autoComplete="off"
        autoFocus
        onBlur={() => window.setTimeout(() => setOpen(false), 120)}
        onChange={(event) => {
          fromPoster.current = false;
          onChange(event.target.value, undefined);
        }}
        onFocus={() => setOpen(suggestions.length > 0)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" && suggestions.length) {
            event.preventDefault();
            setOpen(true);
            setActive((current) => Math.min(current + 1, suggestions.length - 1));
          } else if (event.key === "ArrowUp" && suggestions.length) {
            event.preventDefault();
            setActive((current) => Math.max(current - 1, 0));
          } else if (event.key === "Enter" && open && active >= 0) {
            event.preventDefault();
            choose(suggestions[active]);
          } else if (event.key === "Escape" && open) {
            event.stopPropagation();
            setOpen(false);
          }
        }}
        placeholder="Movie"
        ref={inputRef}
        role="combobox"
        value={movie}
      />
      {open && suggestions.length ? (
        <ul className="suggestions" id={listID} role="listbox">
          {suggestions.map((suggestion, index) => (
            <li aria-selected={index === active} id={`${listID}-${index}`} key={suggestion.id} role="option">
              <button onClick={() => choose(suggestion)} onMouseDown={(event) => event.preventDefault()} tabIndex={-1} type="button">
                <span>{suggestion.title.replace(yearSuffix, "")}</span>
                {suggestion.year ? <small>{suggestion.year}</small> : null}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function DateRange({ start, end, timeZone, zone, onChange, onZone }: {
  start: string;
  end: string;
  timeZone: string;
  zone: string;
  onChange: (start: string, end: string) => void;
  onZone: () => void;
}) {
  const days = Array.from({ length: stripDays }, (_, offset) => dateInTimeZone(timeZone, offset));
  const outsideStrip = start < days[0] || end > days[days.length - 1] || end < start;
  const [calendar, setCalendar] = useState(outsideStrip);
  const [extending, setExtending] = useState(false);

  return (
    <div className="card dates">
      <div className="dates-head">
        <button aria-expanded={calendar} aria-label="Exact dates" className="icon-button" onClick={() => setCalendar((value) => !value)} type="button"><CalendarIcon /></button>
        <span className="dates-summary">{end < start ? rangeLabel.format(noon(start)) : rangeLabel.formatRange(noon(start), noon(end))}</span>
        <button aria-label={`Timezone ${timeZone}`} className="zone-chip" onClick={onZone} title={timeZone} type="button">{zone}</button>
      </div>
      <div aria-hidden="true" className="weekdays">{days.slice(0, 7).map((day) => <span key={day}>{weekdayLetter.format(noon(day))}</span>)}</div>
      <div aria-label="Dates" className="days" role="group">
        {days.map((day, index) => (
          <button
            aria-label={dayLabel.format(noon(day))}
            aria-pressed={day >= start && day <= end}
            className={`day${day >= start && day <= end ? " in-range" : ""}${day === start ? " is-start" : ""}${day === end ? " is-end" : ""}${index === 0 ? " is-today" : ""}`}
            key={day}
            onClick={() => {
              if (extending && day >= start) {
                onChange(start, day);
                setExtending(false);
              } else {
                onChange(day, day);
                setExtending(true);
              }
            }}
            type="button"
          >
            <b>{Number(day.slice(8))}</b>
          </button>
        ))}
      </div>
      <Reveal open={calendar}>
        <div className="date-inputs">
          <input aria-label="From" onChange={(event) => event.target.value && onChange(event.target.value, end < event.target.value ? event.target.value : end)} type="date" value={start} />
          <span aria-hidden="true">→</span>
          <input aria-label="Through" min={start} onChange={(event) => event.target.value && onChange(start, event.target.value)} type="date" value={end} />
        </div>
      </Reveal>
    </div>
  );
}

/** Party size: type the number, or tap the Nth seat to fill 1…N in one move. */
function SeatCount({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  return (
    <div className="field seats">
      <input
        aria-label="Seats"
        className="seat-count"
        inputMode="numeric"
        max={maxTickets}
        min={1}
        // The last typed digit wins, so typing over the current count needs no clearing.
        onChange={(event) => {
          const next = Number(event.target.value.slice(-1));
          if (next >= 1 && next <= maxTickets) onChange(next);
        }}
        onFocus={(event) => event.currentTarget.select()}
        type="number"
        value={value}
      />
      <div aria-hidden="true" className="seat-pick">
        {Array.from({ length: maxTickets }, (_, index) => (
          <button
            className={index < value ? "is-on" : ""}
            key={index}
            onClick={() => onChange(index + 1)}
            onMouseDown={(event) => event.preventDefault()}
            style={{ "--i": index } as React.CSSProperties}
            tabIndex={-1}
            type="button"
          >
            <SeatIcon />
          </button>
        ))}
      </div>
    </div>
  );
}

function ProfilePicker({ value, onChange }: { value: SeatProfile; onChange: (profile: SeatProfile) => void }) {
  return (
    <fieldset className="profile-picker">
      <legend className="sr-only">Seat preference</legend>
      {profiles.map((profile) => (
        <label className="profile-tile" key={profile.value} title={profile.hint}>
          <input checked={value === profile.value} name="seat-profile" onChange={() => onChange(profile.value)} type="radio" value={profile.value} />
          <svg aria-hidden="true" viewBox="0 0 40 32">
            <path className="tile-screen" d="M8 3.5q12-3 24 0" />
            {Array.from({ length: 20 }, (_, index) => <circle className="tile-seat" cx={9 + (index % 5) * 5.5} cy={10 + Math.floor(index / 5) * 5.5} key={index} r="1.3" />)}
            {profile.zones.map(([x, y, width, height], index) => <rect className="tile-zone" height={height * 26} key={index} rx="2" width={width * 32} x={4 + x * 32} y={5 + y * 26} />)}
            {profile.value === "custom" ? <path className="tile-zone tile-draw" d="M11 24c3-9 6-12 9-7s5 3 9-6" /> : null}
          </svg>
          <span>{profile.label}</span>
        </label>
      ))}
    </fieldset>
  );
}

const clampUnit = (value: number) => Math.max(0, Math.min(1, value));
const roundCoordinate = (value: number) => Math.round(clampUnit(value) * 1_000) / 1_000;

function CustomSeatZonePicker({ value, onChange }: { value: NormalizedSeatZone; onChange: (zone: NormalizedSeatZone) => void }) {
  const surface = useRef<HTMLDivElement>(null);
  const dragOrigin = useRef<{ x: number; y: number } | null>(null);

  const pointFromEvent = (event: ReactPointerEvent<HTMLDivElement>) => {
    const bounds = surface.current?.getBoundingClientRect();
    if (!bounds) return null;
    return {
      x: clampUnit((event.clientX - bounds.left) / bounds.width),
      y: clampUnit((event.clientY - bounds.top) / bounds.height),
    };
  };

  const zoneFromPoints = (start: { x: number; y: number }, end: { x: number; y: number }): NormalizedSeatZone => {
    const axis = (first: number, second: number) => {
      let minimum = Math.min(first, second);
      let maximum = Math.max(first, second);
      if (maximum - minimum < .08) {
        const center = (minimum + maximum) / 2;
        minimum = Math.max(0, center - .04);
        maximum = Math.min(1, minimum + .08);
        minimum = Math.max(0, maximum - .08);
      }
      return [roundCoordinate(minimum), roundCoordinate(maximum)] as const;
    };
    const [minimumX, maximumX] = axis(start.x, end.x);
    const [minimumY, maximumY] = axis(start.y, end.y);
    return { minimumX, maximumX, minimumY, maximumY };
  };

  const draw = (event: ReactPointerEvent<HTMLDivElement>) => {
    const point = pointFromEvent(event);
    if (!point || !dragOrigin.current) return;
    onChange(zoneFromPoints(dragOrigin.current, point));
  };

  const moveWithKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? .05 : .025;
    let deltaX = 0;
    let deltaY = 0;
    if (event.key === "ArrowLeft") deltaX = -step;
    else if (event.key === "ArrowRight") deltaX = step;
    else if (event.key === "ArrowUp") deltaY = -step;
    else if (event.key === "ArrowDown") deltaY = step;
    else return;
    event.preventDefault();
    const width = value.maximumX - value.minimumX;
    const height = value.maximumY - value.minimumY;
    const minimumX = clampUnit(Math.min(1 - width, value.minimumX + deltaX));
    const minimumY = clampUnit(Math.min(1 - height, value.minimumY + deltaY));
    onChange({
      minimumX: roundCoordinate(minimumX), maximumX: roundCoordinate(minimumX + width),
      minimumY: roundCoordinate(minimumY), maximumY: roundCoordinate(minimumY + height),
    });
  };

  return (
    <div className="custom-zone">
      <div aria-hidden="true" className="custom-zone-screen" />
      <div
        aria-label="Preferred seat area. Drag to draw; arrow keys move it."
        className="custom-zone-surface"
        onKeyDown={moveWithKeyboard}
        onPointerCancel={() => { dragOrigin.current = null; }}
        onPointerDown={(event) => {
          const point = pointFromEvent(event);
          if (!point) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          dragOrigin.current = point;
          onChange(zoneFromPoints(point, point));
        }}
        onPointerMove={draw}
        onPointerUp={(event) => {
          draw(event);
          dragOrigin.current = null;
          if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        ref={surface}
        role="group"
        tabIndex={0}
      >
        <span
          className="custom-zone-selection"
          style={{
            left: `${value.minimumX * 100}%`,
            top: `${value.minimumY * 100}%`,
            width: `${(value.maximumX - value.minimumX) * 100}%`,
            height: `${(value.maximumY - value.minimumY) * 100}%`,
          }}
        />
      </div>
      <button aria-label="Reset area" className="icon-button custom-zone-reset" onClick={() => onChange(defaultCustomZone)} type="button"><RefreshIcon size={16} /></button>
    </div>
  );
}

/** Height-animated disclosure; closed content stays mounted but inert. */
function Reveal({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <div aria-hidden={!open} className={`reveal${open ? " is-open" : ""}`} inert={!open}>
      <div className="reveal-inner">{children}</div>
    </div>
  );
}

function FilterGroup({ label, icon, children }: { label: string; icon?: ReactNode; children: ReactNode }) {
  return (
    <fieldset className="filter-group">
      <legend>{icon}{label}</legend>
      {children}
    </fieldset>
  );
}

function Segmented({ label, name, options, value, onChange }: { label: string; name: string; options: { value: string; label: string }[]; value: string; onChange: (value: string) => void }) {
  return (
    <div aria-label={label} className="segmented" role="radiogroup">
      {options.map((option) => (
        <label key={option.value}>
          <input checked={value === option.value} name={name} onChange={() => onChange(option.value)} type="radio" value={option.value} />
          <span>{option.label}</span>
        </label>
      ))}
    </div>
  );
}

function Stepper({ label, value, min, max, onChange }: { label: string; value: number; min: number; max: number; onChange: (value: number) => void }) {
  return (
    <div aria-label={label} className="stepper" role="group">
      <span aria-hidden="true" className="stepper-label">{label}</span>
      <button aria-label={`Fewer ${label.toLowerCase()}`} className="icon-button" disabled={value <= min} onClick={() => onChange(value - 1)} type="button"><MinusIcon size={16} /></button>
      <output aria-live="polite" className="stepper-value"><b key={value}>{value}</b></output>
      <button aria-label={`More ${label.toLowerCase()}`} className="icon-button" disabled={value >= max} onClick={() => onChange(value + 1)} type="button"><PlusIcon size={16} /></button>
    </div>
  );
}

function Switch({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <button aria-checked={checked} className="switch" onClick={() => onChange(!checked)} role="switch" type="button">
      <span>{label}</span>
      <i aria-hidden="true" />
    </button>
  );
}

function Range({ label, value, min, max, step = 1, prefix = "", suffix = "", onChange }: { label: string; value: number; min: number; max: number; step?: number; prefix?: string; suffix?: string; onChange: (value: number) => void }) {
  return (
    <label className="range" style={{ "--fill": `${((value - min) / (max - min)) * 100}%` } as React.CSSProperties}>
      <input aria-label={label} max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} step={step} type="range" value={value} />
      <output>{prefix}{value}{suffix}</output>
    </label>
  );
}
