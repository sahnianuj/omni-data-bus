package provision

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeEnv struct {
	version      int
	scopes       map[string]bool
	colls        map[string]bool
	stmts        []string
	forbidColl   map[string]bool
	forbidScope  bool
	forbidIndex  bool
	concurrentAt int
	calls        []string
}

func newFake() *fakeEnv {
	return &fakeEnv{scopes: map[string]bool{}, colls: map[string]bool{}, forbidColl: map[string]bool{}}
}

func (f *fakeEnv) Scope() string                               { return "meta" }
func (f *fakeEnv) Keyspace(c string) string                    { return "`b`.`meta`.`" + c + "`" }
func (f *fakeEnv) CurrentVersion(context.Context) (int, error) { return f.version, nil }
func (f *fakeEnv) SetVersion(_ context.Context, from, to int) error {
	if f.concurrentAt == to {
		f.version = to
		return ErrConcurrent
	}
	if f.version != from {
		return ErrConcurrent
	}
	f.version = to
	return nil
}

func (f *fakeEnv) CreateScope(_ context.Context, s string) error {
	f.calls = append(f.calls, "scope "+s)
	if f.forbidScope {
		return ErrForbidden
	}
	if f.scopes[s] {
		return ErrExists
	}
	f.scopes[s] = true
	return nil
}

func (f *fakeEnv) CreateCollection(_ context.Context, _, c string) error {
	f.calls = append(f.calls, "collection "+c)
	if f.forbidColl[c] {
		return ErrForbidden
	}
	if f.colls[c] {
		return ErrExists
	}
	f.colls[c] = true
	return nil
}

func (f *fakeEnv) WaitCollection(context.Context, string, string) error { return nil }

func (f *fakeEnv) Exec(_ context.Context, stmt string) error {
	if f.forbidIndex {
		return ErrForbidden
	}
	f.stmts = append(f.stmts, stmt)
	return nil
}

func TestSteps_AC1_VersionsSequential(t *testing.T) {
	for i, st := range Steps() {
		require.Equal(t, i+1, st.Version, "versions start at 1 and increase by 1")
		require.NotEmpty(t, st.Name)
		require.NotNil(t, st.Apply)
		require.NotNil(t, st.Plan)
	}
}

func TestRun_AC1_AppliesAllOnEmpty(t *testing.T) {
	env := newFake()
	res, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	require.Equal(t, 0, res.From)
	require.Equal(t, len(Steps()), res.To)
	require.Len(t, env.colls, len(Collections))
	require.Len(t, env.stmts, len(Indexes)+len(JobIndexes))
	require.Contains(t, env.stmts[0], "`b`.`meta`.`projects`", "placeholders expanded")
	require.NotContains(t, strings.Join(env.stmts, " "), "#{")
}

func TestRun_AC1_SkipsAppliedSteps(t *testing.T) {
	env := newFake()
	env.version = 1
	res, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	require.Equal(t, []string{"2 indexes", "3 job indexes"}, res.Applied)
	require.Empty(t, env.colls, "step 1 was not re-run")
}

func TestRun_AC1_SecondRunIsNoop(t *testing.T) {
	env := newFake()
	_, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	res, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	require.Empty(t, res.Applied)
}

func TestRun_AC1_ExistsIsSuccess(t *testing.T) {
	env := newFake()
	env.scopes["meta"] = true
	for _, c := range Collections {
		env.colls[c] = true
	}
	_, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	require.Equal(t, len(Steps()), env.version)
}

func TestRun_AC1_ConcurrentNodeIsTolerated(t *testing.T) {
	env := newFake()
	env.concurrentAt = 1
	res, err := Run(context.Background(), env, Steps())
	require.NoError(t, err)
	require.Equal(t, len(Steps()), res.To)
}

func TestRun_AC8_PermissionDeniedListsMissing(t *testing.T) {
	env := newFake()
	env.forbidScope = true
	env.forbidColl["projects"] = true
	env.forbidColl["users"] = true
	_, err := Run(context.Background(), env, Steps())
	var needs *NeedsAdminError
	require.True(t, errors.As(err, &needs), "got %v", err)
	sort.Strings(needs.Items)
	require.Equal(t, []string{"collection meta.projects", "collection meta.users", "scope meta"}, needs.Items)
	require.Equal(t, 0, env.version, "version is not advanced when something is missing")
	require.Contains(t, err.Error(), "collection meta.projects")
}

func TestRun_AC8_ForbiddenIndexesAreListed(t *testing.T) {
	env := newFake()
	env.forbidIndex = true
	_, err := Run(context.Background(), env, Steps())
	var needs *NeedsAdminError
	require.True(t, errors.As(err, &needs))
	require.Len(t, needs.Items, len(Indexes))
	require.Equal(t, 1, env.version, "collections step still recorded")
}

func TestRun_UnexpectedErrorStops(t *testing.T) {
	env := &errEnv{fakeEnv: newFake()}
	_, err := Run(context.Background(), env, Steps())
	require.ErrorContains(t, err, "boom")
}

type errEnv struct{ *fakeEnv }

func (e *errEnv) CreateCollection(context.Context, string, string) error { return errors.New("boom") }

func TestPlan_AC8_ListsEverything(t *testing.T) {
	items := Plan(newFake(), Steps())
	require.Len(t, items, 1+len(Collections)+len(Indexes)+len(JobIndexes))
	require.Equal(t, "scope meta", items[0])
}
