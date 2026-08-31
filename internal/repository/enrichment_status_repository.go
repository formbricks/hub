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

	// Failed counts records whose last enrichment attempt gave up and which are STILL un-enriched,
	// split by whether retrying could ever help. FailedTerminal is a property of the record's own
	// text, so it will not resolve on its own; Failed can, and is what a retry acts on.
	TranslationFailed         int64
	TranslationFailedTerminal int64
	SentimentFailed           int64
	SentimentFailedTerminal   int64
	EmotionsFailed            int64
	EmotionsFailedTerminal    int64

	TaxonomyEmbeddingPending int64
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

// enrichmentFailedFor builds the two failure predicates for one enrichment from its already-joined
// marker row and its own done-predicate.
//
// A failure counts only while the record is still UN-ENRICHED, and the marker itself is DELETED by
// the successful write (see clearEnrichmentFailure). Both, because they fail differently: the
// delete is what stops a resolved failure resurrecting when a record is later un-enriched — a text
// edit nulls the translation columns, a target_language change shifts the effective target — and
// the un-enriched test is what keeps a marker from contradicting a record that is plainly done.
//
// An earlier attempt used a timestamp instead: count a marker only while failed_at is newer than
// the record's updated_at. It is wrong, and wrong in the direction that hides work. updated_at is
// RECORD-level while markers are per (record, enrichment), so a successful sentiment write bumps
// the timestamp for the emotions and translation markers too and silently drops them from the
// counts. Measured on a real run: 7% of records fell out of the accounted set entirely, which is
// the exact failure this feature exists to prevent. Any future currency rule has to be
// per-enrichment or it will do the same.
func enrichmentFailedFor(alias, notDone string) (failed, terminal string) {
	present := alias + ".feedback_record_id IS NOT NULL AND " + notDone

	return present + " AND NOT " + alias + ".terminal", present + " AND " + alias + ".terminal"
}

// The three done-predicates, negated, as used by the failure counts above.
const (
	enrichmentSentimentNotDone   = `fr.sentiment IS NULL`
	enrichmentEmotionsNotDone    = `NOT ` + enrichmentEmotionsDone
	enrichmentTranslationNotDone = `(fr.translation_lang_key IS DISTINCT FROM ` + enrichmentEffectiveTarget + `)`
)

// enrichmentCountSelect is the shared six-column SELECT list — {sentiment, emotions, translation}
// × {eligible, done} — used by BOTH the per-tenant and aggregate queries so those predicates can't
// drift between them. Column order must match the EnrichmentStatusCounts scan order below. All
// fragments are static constants (never user input).
//
// The failure columns are deliberately NOT here. They are appended only to the per-tenant query
// (see countEnrichmentStatusSQL), because the aggregate's only consumer — the backlog gauge —
// reads eligible and done and nothing else. Sharing them would make the one query documented as a
// full sequential scan build and probe three hash tables per poll and discard every result.
var enrichmentCountSelect = `
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentSentimentOn + ` AND fr.sentiment IS NOT NULL),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEmotionsOn + ` AND ` + enrichmentEmotionsDone + `),
		COUNT(*) FILTER (WHERE ` + enrichmentEligibleText + ` AND ` + enrichmentEffectiveTarget + ` <> ''),
		COUNT(*) FILTER (
			WHERE ` + enrichmentEligibleText + `
			AND ` + enrichmentEffectiveTarget + ` <> ''
			AND fr.translation_lang_key = ` + enrichmentEffectiveTarget + `)`

// failureCountColumns renders the six failure columns, in the same {sentiment, emotions,
// translation} order as the eligible/done columns above. Each pair is gated by the same
// eligibility and per-tenant switch as its enrichment, so a tenant that switched sentiment off
// never reports sentiment failures for work that will not run.
func failureCountColumns() string {
	sentimentFailed, sentimentTerminal := enrichmentFailedFor("fs", enrichmentSentimentNotDone)
	emotionsFailed, emotionsTerminal := enrichmentFailedFor("fe", enrichmentEmotionsNotDone)
	translationFailed, translationTerminal := enrichmentFailedFor("ft", enrichmentTranslationNotDone)

	col := func(gate, predicate string) string {
		return "\n\t\tCOUNT(*) FILTER (WHERE " + enrichmentEligibleText + " AND " + gate + " AND " + predicate + ")"
	}

	return col(enrichmentSentimentOn, sentimentFailed) + "," +
		col(enrichmentSentimentOn, sentimentTerminal) + "," +
		col(enrichmentEmotionsOn, emotionsFailed) + "," +
		col(enrichmentEmotionsOn, emotionsTerminal) + "," +
		col(enrichmentEffectiveTarget+" <> ''", translationFailed) + "," +
		col(enrichmentEffectiveTarget+" <> ''", translationTerminal)
}

// enrichmentCountFrom joins the failure markers once per enrichment. Each join hits the table's
// primary key (feedback_record_id, enrichment) and can match at most one row, so this stays a
// lookup rather than a fan-out — the counts would be wrong if it did not.
//
// The join is on feedback_record_id ONLY. The markers carry a tenant_id column, but it exists for
// its own index and is never the tenant boundary: that stays fr.tenant_id in the outer WHERE, so
// the boundary has exactly one source of truth. See migration 022.
const enrichmentCountFrom = `
	FROM feedback_records fr
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id`

// enrichmentFailureJoins attaches the marker rows, one join per enrichment. Appended ONLY to the
// per-tenant query: the aggregate does not read the failure columns, and adding three joins to a
// whole-table sequential scan to discard the results is the kind of cost that only shows up in
// production.
//
// Each join hits the markers' primary key (feedback_record_id, enrichment) and can match at most
// one row, so this stays a lookup rather than a fan-out — the eligible and done counts would be
// wrong if it did not.
//
// THE TENANT BOUNDARY IS STILL fr.tenant_id IN THE OUTER WHERE. The markers' own tenant_id appears
// here too, and the distinction matters: it is an ADDITIONAL predicate, never a replacement. A
// join predicate can only narrow which marker attaches to a record, so it cannot pull in another
// tenant's row; drop `fr.tenant_id = $n` and filter on the stamp instead and the boundary moves to
// a denormalized column that is only as good as the write path that filled it. See migration 022.
//
// It is here for the index. Without it the planner has no tenant-side selectivity on the marker
// table and hash-joins the WHOLE table three times, so a tenant with 2,500 records and no failures
// of its own pays for every other tenant's: measured at 1M records / 300k markers, 3ms on the
// pre-failure query became 32ms, scaling with the GLOBAL marker count rather than the tenant's.
// One tenant's provider outage slowing everyone else's status endpoint is the part that made this
// worth a predicate. With it, idx_enrichment_failures_tenant_enrichment applies and the same query
// is 6ms.
//
// The trade is that a marker whose stamp disagreed with its record's tenant would stop being
// counted. Nothing can produce that — one write site, tenant read from the record, and ON CONFLICT
// does not update it — and under-counting is the safe direction: the record simply reads as still
// in progress, which is what re-enqueueing it will fix.
//
// tenantParam must be the placeholder the outer WHERE uses for the tenant, so the two cannot drift.
func enrichmentFailureJoins(tenantParam string) string {
	join := func(alias, enrichment string) string {
		return `
	LEFT JOIN feedback_record_enrichment_failures ` + alias + `
		ON ` + alias + `.feedback_record_id = fr.id
		AND ` + alias + `.tenant_id = ` + tenantParam + `
		AND ` + alias + `.enrichment = '` + enrichment + `'`
	}

	return join("fs", "sentiment") + join("fe", "emotions") + join("ft", "translation")
}

// The `fr.field_type = 'text'` predicate in the outer WHERE of both queries below is redundant
// with enrichmentEligibleText inside every FILTER (so it can never change a count); it is hoisted
// out so the planner can use it as an access-path predicate rather than only as a per-row filter.

// countEnrichmentStatusSQL counts eligible/done per enrichment for ONE tenant. $1 = deployment
// default target language, $2 = tenant_id. The (tenant_id, field_type) pair matches
// idx_feedback_records_tenant_field_type, so cost scales with the tenant's text-record count.
var countEnrichmentStatusSQL = `SELECT ` + enrichmentCountSelect + `,` + failureCountColumns() +
	enrichmentCountFrom + enrichmentFailureJoins(`$2`) + `
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
var countEnrichmentBacklogAggregateSQL = `SELECT ` + enrichmentCountSelect + enrichmentCountFrom + `
	WHERE fr.field_type = 'text'`

// countTaxonomyEmbeddingBacklogAggregateSQL counts text records eligible for the
// taxonomy-translated input but still missing the exact configured taxonomy model. field_type is
// load-bearing here: only text records enter the live translation-to-taxonomy pipeline, so counting
// categorical rows carrying value_text would create a permanent non-zero gauge floor. $1 = taxonomy
// embedding model.
const countTaxonomyEmbeddingBacklogAggregateSQL = `
	SELECT COUNT(*)
	FROM feedback_records fr
	WHERE fr.field_type = 'text'
	  AND COALESCE(NULLIF(btrim(fr.value_text_translated), ''), NULLIF(btrim(fr.value_text), '')) IS NOT NULL
	  AND NOT EXISTS (
		SELECT 1
		FROM embeddings e
		WHERE e.feedback_record_id = fr.id AND e.model = $1
	  )`

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
	return scanEnrichmentCountsWithFailures(
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
	ctx context.Context, defaultLang, taxonomyEmbeddingModel string,
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

	if taxonomyEmbeddingModel != "" {
		if err := l.conn.QueryRow(
			ctx, countTaxonomyEmbeddingBacklogAggregateSQL, taxonomyEmbeddingModel,
		).Scan(&counts.TaxonomyEmbeddingPending); err != nil {
			l.release(ctx)

			return EnrichmentStatusCounts{}, false, fmt.Errorf("count taxonomy embedding backlog aggregate: %w", err)
		}
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

// release relinquishes leadership and disposes of the leader connection.
//
// Both cleanup statements undo SESSION state that would otherwise ride the connection back into the
// pool: the advisory lock (which survives Release and would wedge leadership for every replica) and
// the leader-only idle timeout (which would let Postgres terminate whichever component borrowed the
// connection next and left it idle). Each gets its OWN deadline, detached from the caller's context
// so shutdown -- whose context is already cancelled -- still cleans up, and so a slow unlock cannot
// eat the budget the reset needs.
//
// If either statement does not complete we cannot prove the session is clean, so the connection is
// taken out of the pool (Hijack + Close) rather than handed to an unrelated caller. Losing one
// pooled connection is strictly cheaper than leaking a lock or a timeout onto a shared one.
func (l *EnrichmentBacklogLeader) release(ctx context.Context) {
	if l.conn == nil {
		return
	}

	conn := l.conn
	l.conn = nil

	unlockErr := l.execDetached(ctx, conn, sessionUnlockSQL, enrichmentBacklogLockKey)
	resetErr := l.execDetached(ctx, conn, resetLeaderIdleTimeoutSQL)

	if unlockErr != nil || resetErr != nil {
		slog.WarnContext(ctx, "enrichment backlog: leader session cleanup incomplete, discarding connection",
			"unlock_error", unlockErr, "reset_error", resetErr)

		// Hijack removes the connection from the pool and transfers ownership here; closing it ends
		// the backend session, which releases anything the statements above failed to undo.
		hijacked := conn.Hijack()

		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enrichmentBacklogUnlockTimeout)
		defer cancel()

		_ = hijacked.Close(closeCtx)

		return
	}

	conn.Release()
}

// execDetached runs one cleanup statement on its own deadline, independent of the caller's context.
func (l *EnrichmentBacklogLeader) execDetached(
	ctx context.Context, conn *pgxpool.Conn, sql string, args ...any,
) error {
	execCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enrichmentBacklogUnlockTimeout)
	defer cancel()

	if _, err := conn.Exec(execCtx, sql, args...); err != nil {
		return fmt.Errorf("enrichment backlog leader cleanup: %w", err)
	}

	return nil
}

// scanEnrichmentCounts reads the six-column eligible/done row; the scan order matches
// enrichmentCountSelect. Used by the aggregate query, which selects only those columns.
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

// scanEnrichmentCountsWithFailures reads the twelve-column row the per-tenant query returns: the
// six above followed by failureCountColumns, in the same {sentiment, emotions, translation} order.
// The two scanners exist because only the per-tenant query selects the failure half — see
// enrichmentCountSelect.
func scanEnrichmentCountsWithFailures(row pgx.Row, what string) (EnrichmentStatusCounts, error) {
	var counts EnrichmentStatusCounts

	if err := row.Scan(
		&counts.SentimentEligible, &counts.SentimentDone,
		&counts.EmotionsEligible, &counts.EmotionsDone,
		&counts.TranslationEligible, &counts.TranslationDone,
		&counts.SentimentFailed, &counts.SentimentFailedTerminal,
		&counts.EmotionsFailed, &counts.EmotionsFailedTerminal,
		&counts.TranslationFailed, &counts.TranslationFailedTerminal,
	); err != nil {
		return EnrichmentStatusCounts{}, fmt.Errorf("%s: %w", what, err)
	}

	return counts, nil
}

// FailedRecordCount is one (enrichment, terminal) bucket of the cross-tenant failure gauge.
type FailedRecordCount struct {
	Enrichment string
	Terminal   bool
	Count      int64
}

// countFailedRecordsAggregateSQL counts failure markers whose record is STILL un-enriched and still
// owed the enrichment, across all tenants, grouped by enrichment and permanence.
//
// Gated the same three ways the endpoint and the neighbouring backlog gauge are, because a gauge
// that counts work nobody will ever do is worse than no gauge: an operator sees failures that the
// status endpoint reports as zero for the same tenant and has no way to reconcile the two.
//
//   - eligibility (enrichmentEligibleText) — a record whose text was blanked is owed nothing
//   - the tenant switch — sentiment/emotions turned off for a directory means those failures are
//     not work, they are history
//
// Translation's un-enriched test stays deliberately weaker than the endpoint's: that one compares
// against the tenant's EFFECTIVE target, which needs the deployment default, and a gauge summed
// across tenants has no single target to compare against. It asks only whether the record has any
// translation at all. A record translated into a since-changed target therefore counts as done here
// while the endpoint counts it as pending — acceptable deployment-wide, and written down so the two
// disagreeing is not mistaken for a bug. The switch and eligibility gates carry no such excuse,
// which is why they are here.
//
// Still cheap relative to the eligible/done aggregate above. That one must scan every feedback
// record; here the driving table is the small one — the markers, of which there are by definition
// few — so it stays a scan of failures with primary-key lookups back to the record and its
// settings.
var countFailedRecordsAggregateSQL = `
	SELECT f.enrichment, f.terminal, COUNT(*)
	FROM feedback_record_enrichment_failures f
	JOIN feedback_records fr ON fr.id = f.feedback_record_id
	LEFT JOIN tenant_settings ts ON ts.tenant_id = fr.tenant_id
	WHERE ` + enrichmentEligibleText + `
	  AND ((f.enrichment = 'sentiment'   AND fr.sentiment IS NULL AND ` + enrichmentSentimentOn + `)
	    OR (f.enrichment = 'emotions'    AND fr.emotions_classified_at IS NULL AND fr.emotions IS NULL
	                                     AND ` + enrichmentEmotionsOn + `)
	    OR (f.enrichment = 'translation' AND fr.translation_lang_key IS NULL)
	    OR (f.enrichment = 'taxonomy_embedding'
	        AND $1 <> ''
	        AND f.context_key = $1
	        AND f.source_updated_at = fr.updated_at
	        AND NOT EXISTS (
	          SELECT 1 FROM embeddings e
	          WHERE e.feedback_record_id = fr.id AND e.model = $1
	        )))
	GROUP BY f.enrichment, f.terminal`

// CountFailedRecordsAggregate returns the cross-tenant failed-record counts per enrichment.
//
// Translation's un-enriched test is deliberately weaker than the per-tenant endpoint's: that one
// compares against the tenant's EFFECTIVE target, which needs the settings join and the deployment
// default. A gauge summed across tenants has no single target to compare against, so it asks only
// whether the record has any translation at all. The consequence is that a record translated into
// a since-changed target counts as done here while the endpoint counts it as pending — acceptable
// for a deployment-wide gauge, and written down so the two are not mistaken for a bug when they
// disagree.
func (r *EnrichmentStatusRepository) CountFailedRecordsAggregate(
	ctx context.Context, taxonomyEmbeddingModel string,
) ([]FailedRecordCount, error) {
	rows, err := r.db.Query(ctx, countFailedRecordsAggregateSQL, taxonomyEmbeddingModel)
	if err != nil {
		return nil, fmt.Errorf("count failed records: %w", err)
	}
	defer rows.Close()

	var counts []FailedRecordCount

	for rows.Next() {
		var count FailedRecordCount
		if scanErr := rows.Scan(&count.Enrichment, &count.Terminal, &count.Count); scanErr != nil {
			return nil, fmt.Errorf("scan failed records: %w", scanErr)
		}

		counts = append(counts, count)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read failed records: %w", err)
	}

	return counts, nil
}
