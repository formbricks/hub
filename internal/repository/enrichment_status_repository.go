package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnrichmentStatusRepository computes data-derived enrichment progress counts over
// feedback_records. It is a read-only sibling of FeedbackRecordsRepository, kept separate
// so the status/observability concern doesn't grow the primary records repository.
type EnrichmentStatusRepository struct {
	db *pgxpool.Pool
}

// NewEnrichmentStatusRepository creates an enrichment status repository.
func NewEnrichmentStatusRepository(db *pgxpool.Pool) *EnrichmentStatusRepository {
	return &EnrichmentStatusRepository{db: db}
}

// EnrichmentStatusCounts holds the raw per-enrichment eligible/done counts. The counts already
// reflect the per-tenant gates: sentiment/emotions include only tenants with the enrichment
// switched on, translation only records with a resolvable effective target language. The
// deployment-level (provider/model) gate is applied by the caller.
type EnrichmentStatusCounts struct {
	TranslationEligible int64
	TranslationDone     int64
	SentimentEligible   int64
	SentimentDone       int64
	EmotionsEligible    int64
	EmotionsDone        int64
}

// enrichmentEligibleText is the data-level eligibility predicate: an open-text field with content.
//
// It trims the full ASCII whitespace set (space, tab, VT, FF, CR, LF). This deliberately does NOT
// match the backfill queries (classifyBackfillEligibleSQL / translationBackfillSelectSQL), whose
// bare btrim() strips spaces only: a value of "\t\n" is enqueued by those but counted ineligible
// here. That asymmetry is intentional -- what matters for a progress count is agreeing with the
// WORKER, which gates on Go strings.TrimSpace (HasOpenText) and would clear such a record rather
// than enrich it. Counting it eligible would leave it pending forever. Do not "restore parity" by
// weakening this to bare btrim.
//
// It remains an approximation in one direction: strings.TrimSpace also strips exotic Unicode
// whitespace (NBSP U+00A0, ideographic space U+3000, ...), so a value composed ENTIRELY of those is
// still counted eligible while the worker treats it as empty. Rare enough to accept; expressing the
// full Unicode set here would mean embedding invisible characters in this source file.
//
// field_type = 'text' is load-bearing: matrix/multi-choice expansion writes value_text on
// categorical/number rows that are not enrichable.
const enrichmentEligibleText = `fr.field_type = 'text' AND fr.value_text IS NOT NULL AND btrim(fr.value_text, E' \t\n\v\f\r') <> ''`

// enrichmentEffectiveTarget resolves a tenant's effective translation target: its own
// target_language, falling back to the deployment default ($1). An empty result means translation
// is not enabled for the tenant. Mirrors translationBackfillSelectSQL.
// enrichmentSentimentOn / enrichmentEmotionsOn read the tri-state per-directory switch, defaulting
// to enabled when the key is absent — matching EnrichmentSettings.SentimentEnrichmentEnabled /
// EmotionsEnrichmentEnabled (parity is covered by test). $1 is the deployment default target in
// both the per-tenant and aggregate queries.
const (
	enrichmentEffectiveTarget = `COALESCE(NULLIF(ts.settings->>'target_language', ''), $1)`
	enrichmentSentimentOn     = `COALESCE((ts.settings->>'sentiment_enabled')::boolean, true)`
	enrichmentEmotionsOn      = `COALESCE((ts.settings->>'emotions_enabled')::boolean, true)`
)

// enrichmentEmotionsDone reports that emotion classification has completed for a record. It cannot
// be `emotions IS NOT NULL` alone: a successful classification that detects no emotion is stored as
// NULL (the 015 CHECK rejects the empty array), so such records would count as pending forever and
// the backlog would never drain. emotions_classified_at (migration 020) records completion
// independently of the labels found. The `emotions IS NOT NULL` arm covers rows classified BEFORE
// that column existed -- a non-NULL label set is itself proof of completion -- which is why the
// migration needs no bulk backfill; only historical classified-empty rows remain unresolved until
// the classify backfill re-processes them.
const enrichmentEmotionsDone = `(fr.emotions_classified_at IS NOT NULL OR fr.emotions IS NOT NULL)`

// enrichmentCountSelect is the shared six-column SELECT list — {sentiment, emotions, translation} ×
// {eligible, done} — used by both the per-tenant and aggregate queries so the predicates can't
// drift between them. Column order must match the EnrichmentStatusCounts scan order below. All
// fragments are static constants (never user input).
const enrichmentCountSelect = `
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + ` AND fr.sentiment IS NOT NULL),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + ` AND ` + enrichmentEmotionsDone + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEffectiveTarget + ` <> ''),
		COUNT(*) FILTER (
			WHERE ` + enrichmentEligibleText + `
			AND ` + enrichmentEffectiveTarget + ` <> ''
			AND fr.translation_lang_key = ` + enrichmentEffectiveTarget + `)`

const enrichmentCountFrom = `
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id`

// The `fr.field_type = 'text'` predicate in the outer WHERE of both queries below is redundant
// with enrichmentEligibleText inside every FILTER (so it can never change a count); it is hoisted
// out so the planner can use it as an access-path predicate rather than only as a per-row filter.

// countEnrichmentStatusSQL counts eligible/done per enrichment for ONE tenant. $1 = deployment
// default target language, $2 = tenant_id. The (tenant_id, field_type) pair matches
// idx_feedback_records_tenant_field_type, so cost scales with the tenant's text-record count.
const countEnrichmentStatusSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.tenant_id = $2 AND fr.field_type = 'text'`

// countEnrichmentBacklogAggregateSQL is the same SELECT without the tenant filter: it sums
// eligible/done per enrichment across ALL tenants (for the observability gauge). $1 = deployment
// default target language. The per-tenant enable gates still apply, so a tenant that switched an
// enrichment off, or has no resolvable target, never inflates the backlog.
//
// NOTE: unlike the per-tenant query this one canNOT use idx_feedback_records_tenant_field_type --
// tenant_id is that index's leading column and this query has no tenant predicate -- so Postgres
// plans a sequential scan of feedback_records (confirmed via EXPLAIN). Accepted rather than fixed
// with a new index: the aggregate has to read every text row regardless, so an index scan covering
// most of the table would not be cheaper. (Migration 016 does already maintain partial indexes over
// unenriched text rows, so a further index is not unthinkable -- it is simply unlikely to pay off
// for a whole-table aggregate.) The cost is instead bounded by running the scan infrequently
// (enrichmentBacklogInterval), under a statement timeout, and on exactly ONE replica via the
// leader election below.
const countEnrichmentBacklogAggregateSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.field_type = 'text'`

// enrichmentBacklogLockKey names the advisory lock that elects the single backlog-poller process
// across API replicas. Hashed with hashtextextended like the other advisory locks in this package.
const enrichmentBacklogLockKey = "hub:enrichment-backlog-poller"

// enrichmentBacklogUnlockTimeout bounds the best-effort advisory unlock on shutdown.
const enrichmentBacklogUnlockTimeout = 5 * time.Second

const (
	// trySessionLockSQL takes a SESSION-scoped advisory lock without blocking. Session scope is
	// deliberate -- see EnrichmentBacklogLeader for why a transaction-scoped lock cannot work here.
	trySessionLockSQL = `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`
	// sessionUnlockSQL releases it. A session lock outlives returning the connection to the pool,
	// so it must be released explicitly.
	sessionUnlockSQL = `SELECT pg_advisory_unlock(hashtextextended($1, 0))`
	// setLeaderIdleTimeoutSQL caps how long a stalled leader session can hold the lock. Applies to
	// this session only (SET, not ALTER ROLE); comfortably above enrichmentBacklogInterval so a
	// healthy leader, which queries every interval, is never affected. Requires PG14+.
	//
	// It cannot be SET LOCAL (the convention elsewhere in this package, e.g. embeddings_repository)
	// because that is transaction-scoped and the leader deliberately holds no transaction -- which
	// is exactly why release() must RESET it before the connection returns to the pool.
	setLeaderIdleTimeoutSQL = `SET idle_session_timeout = '30min'`
	// resetLeaderIdleTimeoutSQL restores the server default so the pooled connection goes back clean.
	resetLeaderIdleTimeoutSQL = `RESET idle_session_timeout`
)

// CountEnrichmentStatus returns one tenant's eligible/done counts per enrichment. defaultLang is
// the deployment translation fallback ("" disables the fallback, so only tenants with their own
// target language have eligible translation records). Always scoped to the given tenant_id.
func (r *EnrichmentStatusRepository) CountEnrichmentStatus(
	ctx context.Context, tenantID, defaultLang string,
) (EnrichmentStatusCounts, error) {
	return scanEnrichmentCounts(
		r.db.QueryRow(ctx, countEnrichmentStatusSQL, defaultLang, tenantID), "count enrichment status")
}

// CountEnrichmentBacklogAggregate returns eligible/done counts per enrichment summed across all
// tenants. defaultLang is the deployment translation fallback. The result carries no tenant
// dimension. Prefer CountEnrichmentBacklogAggregateIfLeader from the poller so only one replica
// runs the scan.
func (r *EnrichmentStatusRepository) CountEnrichmentBacklogAggregate(
	ctx context.Context, defaultLang string,
) (EnrichmentStatusCounts, error) {
	return scanEnrichmentCounts(
		r.db.QueryRow(ctx, countEnrichmentBacklogAggregateSQL, defaultLang), "count enrichment backlog aggregate")
}

// EnrichmentBacklogLeader elects ONE process to refresh the cross-tenant backlog gauge.
//
// Production runs several API replicas per region. Without election each replica repeats the same
// full-table aggregate every tick and exports its own copy of a value that is global by definition:
// N times the DB work, and N identical series that a dashboard summing them silently over-counts.
//
// Leadership is a SESSION-scoped advisory lock held on a dedicated pooled connection for the
// process lifetime, NOT a lock taken around each scan. That distinction is the whole point:
// replicas tick on independent, unsynchronized schedules (each ticker starts at its own boot), and
// a scan-scoped lock is held for only a couple of seconds out of every interval, so replicas would
// virtually never collide -- suppressing nothing, while occasionally blanking one replica's series
// when they did collide. Sticky leadership instead means exactly one replica scans and exports, and
// the series stays put instead of flapping between replicas.
//
// A non-leader never holds a connection: it acquires one, loses the race, and hands it straight
// back. If the leader's connection dies its backend session ends and Postgres drops the lock
// automatically, so the next tick re-elects; the process also drops leadership itself whenever a
// scan fails, so it cannot keep believing it is the leader after losing the session.
type EnrichmentBacklogLeader struct {
	pool *pgxpool.Pool
	conn *pgxpool.Conn // non-nil only while this process holds leadership
}

// NewEnrichmentBacklogLeader creates a leader-elected reader for the aggregate backlog counts.
func NewEnrichmentBacklogLeader(pool *pgxpool.Pool) *EnrichmentBacklogLeader {
	return &EnrichmentBacklogLeader{pool: pool}
}

// CountIfLeader returns the cross-tenant counts when this process holds (or wins) leadership, and
// reports whether it did. Not being the leader is the normal steady state for all but one replica,
// so it is signalled by a false second return rather than an error.
func (l *EnrichmentBacklogLeader) CountIfLeader(
	ctx context.Context, defaultLang string,
) (EnrichmentStatusCounts, bool, error) {
	if l.conn == nil {
		acquired, err := l.tryAcquire(ctx)
		if err != nil || !acquired {
			return EnrichmentStatusCounts{}, false, err
		}
	}

	// Deliberately run on the leader connection: a broken session then surfaces here as a scan
	// error rather than silently leaving this process convinced it still holds the lock.
	counts, err := scanEnrichmentCounts(
		l.conn.QueryRow(ctx, countEnrichmentBacklogAggregateSQL, defaultLang), "count enrichment backlog aggregate")
	if err != nil {
		l.release(ctx)

		return EnrichmentStatusCounts{}, false, err
	}

	return counts, true, nil
}

// Close relinquishes leadership so another replica can take over promptly instead of waiting for
// this process's session to time out. Safe to call when not the leader.
func (l *EnrichmentBacklogLeader) Close(ctx context.Context) {
	l.release(ctx)
}

// tryAcquire takes a connection and attempts the session lock, keeping the connection only on
// success -- a non-leader must not pin a pooled connection it will not use.
func (l *EnrichmentBacklogLeader) tryAcquire(ctx context.Context) (bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire enrichment backlog leader connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, trySessionLockSQL, enrichmentBacklogLockKey).Scan(&acquired); err != nil {
		conn.Release()

		return false, fmt.Errorf("try enrichment backlog advisory lock: %w", err)
	}

	if !acquired {
		conn.Release()

		return false, nil
	}

	// Bound how long a LOST leader can keep the lock. A session lock lives until its backend exits;
	// a graceful shutdown releases it via Close, and a killed pod's socket closes, but a node
	// failure or network partition leaves a zombie backend holding it until TCP keepalives reap the
	// connection -- hours under Linux defaults, during which no replica can take over and the gauge
	// just goes absent. An idle timeout on this session alone caps that at one timeout period. The
	// leader queries every enrichmentBacklogInterval, so the timeout is set well above it and can
	// only fire on a session that has genuinely stopped polling. Best effort: on an older server or
	// a restricted role this simply does not apply and behaviour is as before.
	if _, err := conn.Exec(ctx, setLeaderIdleTimeoutSQL); err != nil {
		slog.WarnContext(ctx, "enrichment backlog: could not bound leader session idle timeout",
			"error", err)
	}

	l.conn = conn

	return true, nil
}

// release unlocks and returns the leader connection. The unlock uses a context detached from the
// caller's, so shutdown (whose context is already cancelled) still releases the lock rather than
// leaving it held until the backend session is reaped.
func (l *EnrichmentBacklogLeader) release(ctx context.Context) {
	if l.conn == nil {
		return
	}

	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enrichmentBacklogUnlockTimeout)
	defer cancel()

	// Best effort: if this fails the connection is broken, and ending that session releases the
	// lock anyway. Returning the connection WITHOUT unlocking would be the real leak, since a
	// session lock survives being handed back to the pool.
	_, _ = l.conn.Exec(unlockCtx, sessionUnlockSQL, enrichmentBacklogLockKey)

	// Undo the leader-only idle timeout for the same reason: SET is session state, and the pool
	// hands this exact backend to unrelated callers afterwards (verified: same pid, setting still
	// in effect). Leaving it behind would let Postgres terminate some other component's pooled
	// connection after 30 idle minutes.
	_, _ = l.conn.Exec(unlockCtx, resetLeaderIdleTimeoutSQL)

	l.conn.Release()
	l.conn = nil
}

// scanEnrichmentCounts reads the six-column count row; the scan order matches enrichmentCountSelect.
func scanEnrichmentCounts(row pgx.Row, what string) (EnrichmentStatusCounts, error) {
	var counts EnrichmentStatusCounts

	if err := row.Scan(
		&counts.SentimentEligible, &counts.SentimentDone,
		&counts.EmotionsEligible, &counts.EmotionsDone,
		&counts.TranslationEligible, &counts.TranslationDone,
	); err != nil {
		return EnrichmentStatusCounts{}, fmt.Errorf("%s: %w", what, err)
	}

	return counts, nil
}
