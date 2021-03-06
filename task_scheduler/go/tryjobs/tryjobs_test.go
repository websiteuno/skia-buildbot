package tryjobs

import (
	"fmt"
	"sort"
	"testing"
	"time"

	buildbucket_api "github.com/luci/luci-go/common/api/buildbucket/buildbucket/v1"
	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/buildbucket"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/task_scheduler/go/db"
)

// Verify that updateJobs sends heartbeats for unfinished try Jobs and
// success/failure for finished Jobs.
func TestUpdateJobs(t *testing.T) {
	trybots, gb, mock, cleanup := setup(t)
	defer cleanup()

	now := time.Now()

	assertActiveTryJob := func(j *db.Job) {
		assert.NoError(t, trybots.jCache.Update())
		active, err := trybots.jCache.GetActiveTryJobs()
		assert.NoError(t, err)
		expect := []*db.Job{}
		if j != nil {
			expect = append(expect, j)
		}
		testutils.AssertDeepEqual(t, expect, active)
	}
	assertNoActiveTryJobs := func() {
		assertActiveTryJob(nil)
	}

	// No jobs.
	assertNoActiveTryJobs()
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())

	// One unfinished try job.
	j1 := tryjob(gb.RepoUrl())
	MockHeartbeats(t, mock, now, []*db.Job{j1}, nil)
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j1}))
	assert.NoError(t, trybots.jCache.Update())
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())
	assertActiveTryJob(j1)

	// Send success/failure for finished jobs, not heartbeats.
	j1.Status = db.JOB_STATUS_SUCCESS
	j1.Finished = now
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j1}))
	assert.NoError(t, trybots.jCache.Update())
	MockJobSuccess(mock, j1, now, nil, false)
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())
	assertNoActiveTryJobs()

	// Failure.
	j1, err := trybots.db.GetJobById(j1.Id)
	assert.NoError(t, err)
	j1.BuildbucketLeaseKey = 12345
	j1.Status = db.JOB_STATUS_FAILURE
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j1}))
	assert.NoError(t, trybots.jCache.Update())
	MockJobFailure(mock, j1, now, nil)
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())
	assertNoActiveTryJobs()

	// More than one batch of heartbeats.
	jobs := []*db.Job{}
	for i := 0; i < LEASE_BATCH_SIZE+2; i++ {
		jobs = append(jobs, tryjob(gb.RepoUrl()))
	}
	sort.Sort(db.JobSlice(jobs))
	MockHeartbeats(t, mock, now, jobs[:LEASE_BATCH_SIZE], nil)
	MockHeartbeats(t, mock, now, jobs[LEASE_BATCH_SIZE:], nil)
	assert.NoError(t, trybots.db.PutJobs(jobs))
	assert.NoError(t, trybots.jCache.Update())
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())

	// Test heartbeat failure for one job, ensure that it gets canceled.
	j1, j2 := jobs[0], jobs[1]
	for _, j := range jobs[2:] {
		j.Status = db.JOB_STATUS_SUCCESS
		j.Finished = time.Now()
	}
	assert.NoError(t, trybots.db.PutJobs(jobs[2:]))
	assert.NoError(t, trybots.jCache.Update())
	for _, j := range jobs[2:] {
		MockJobSuccess(mock, j, now, nil, false)
	}
	MockHeartbeats(t, mock, now, []*db.Job{j1, j2}, map[string]*heartbeatResp{
		j1.Id: &heartbeatResp{
			BuildId: fmt.Sprintf("%d", j1.BuildbucketBuildId),
			Error: &errMsg{
				Message: "fail",
			},
		},
	})
	assert.NoError(t, trybots.updateJobs(now))
	assert.True(t, mock.Empty())
	assert.NoError(t, trybots.jCache.Update())
	active, err := trybots.jCache.GetActiveTryJobs()
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, []*db.Job{j2}, active)
	canceled, err := trybots.jCache.db.GetJobById(j1.Id)
	assert.NoError(t, err)
	assert.True(t, canceled.Done())
	assert.Equal(t, db.JOB_STATUS_CANCELED, canceled.Status)
}

func TestGetRepo(t *testing.T) {
	trybots, _, _, cleanup := setup(t)
	defer cleanup()

	props := &buildbucket.Properties{
		PatchProject: patchProject,
	}

	// Test basic.
	url, r, patchRepo, err := trybots.getRepo(props)
	assert.NoError(t, err)
	repo := trybots.projectRepoMapping[patchProject]
	assert.Equal(t, repo, url)
	assert.Equal(t, repo, patchRepo)
	assert.NotNil(t, r)

	// Bogus repo.
	props.PatchProject = "bogus"
	_, _, _, err = trybots.getRepo(props)
	assert.EqualError(t, err, "Unknown patch project \"bogus\"")

	// Cross-repo try job.
	parentUrl := trybots.projectRepoMapping[parentProject]
	props.PatchProject = patchProject
	props.TryJobRepo = parentUrl
	url, r, patchRepo, err = trybots.getRepo(props)
	assert.NoError(t, err)
	assert.Equal(t, parentUrl, url)
	assert.Equal(t, repo, patchRepo)
}

func TestGetRevision(t *testing.T) {
	trybots, _, mock, cleanup := setup(t)
	defer cleanup()

	// Get the (only) commit from the repo.
	props := &buildbucket.Properties{
		PatchProject: patchProject,
	}
	_, r, _, err := trybots.getRepo(props)
	assert.NoError(t, err)
	c, err := r.Repo().RevParse("origin/master")
	assert.NoError(t, err)

	// Fake response from Gerrit.
	ci := &gerrit.ChangeInfo{
		Branch: "master",
	}
	serialized := []byte(testutils.MarshalJSON(t, ci))
	// Gerrit API prepends garbage to prevent XSS.
	serialized = append([]byte("abcd\n"), serialized...)
	url := fmt.Sprintf("%s/a/changes/%d/detail?o=ALL_REVISIONS", fakeGerritUrl, gerritIssue)
	mock.Mock(url, mockhttpclient.MockGetDialogue(serialized))

	// Try different inputs to getRevision.
	tests := map[string]string{
		"":              c,
		"HEAD":          c,
		"master":        c,
		"origin/master": c,
		c:               c,
		"abc123":        "",
	}
	for input, expect := range tests {
		got, err := trybots.getRevision(r, input, gerritIssue)
		if expect == "" {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
			assert.Equal(t, expect, got, fmt.Sprintf("Input: %q", input))
		}
	}
}

func TestCancelBuild(t *testing.T) {
	trybots, _, mock, cleanup := setup(t)
	defer cleanup()

	id := int64(12345)
	MockCancelBuild(mock, id, nil)
	assert.NoError(t, trybots.remoteCancelBuild(id, "Canceling!"))
	assert.True(t, mock.Empty())

	err := fmt.Errorf("Build does not exist!")
	MockCancelBuild(mock, id, err)
	assert.EqualError(t, trybots.remoteCancelBuild(id, "Canceling!"), err.Error())
	assert.True(t, mock.Empty())
}

func TestTryLeaseBuild(t *testing.T) {
	trybots, _, mock, cleanup := setup(t)
	defer cleanup()

	id := int64(12345)
	now := time.Now()
	MockTryLeaseBuild(mock, id, now, nil)
	k, err := trybots.tryLeaseBuild(id, now)
	assert.NoError(t, err)
	assert.NotEqual(t, k, 0)
	assert.True(t, mock.Empty())

	expect := fmt.Errorf("Can't lease this!")
	MockTryLeaseBuild(mock, id, now, expect)
	_, err = trybots.tryLeaseBuild(id, now)
	assert.EqualError(t, err, expect.Error())
	assert.True(t, mock.Empty())
}

func TestJobStarted(t *testing.T) {
	trybots, gb, mock, cleanup := setup(t)
	defer cleanup()

	j := tryjob(gb.RepoUrl())
	now := time.Now()

	// Success
	MockJobStarted(mock, j.BuildbucketBuildId, now, nil)
	assert.NoError(t, trybots.jobStarted(j))
	assert.True(t, mock.Empty())

	// Failure
	err := fmt.Errorf("fail")
	MockJobStarted(mock, j.BuildbucketBuildId, now, err)
	assert.EqualError(t, trybots.jobStarted(j), err.Error())
	assert.True(t, mock.Empty())
}

func TestJobFinished(t *testing.T) {
	trybots, gb, mock, cleanup := setup(t)
	defer cleanup()

	j := tryjob(gb.RepoUrl())
	now := time.Now()

	// Job not actually finished.
	assert.EqualError(t, trybots.jobFinished(j), "JobFinished called for unfinished Job!")

	// Successful job.
	j.Status = db.JOB_STATUS_SUCCESS
	j.Finished = now
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j}))
	assert.NoError(t, trybots.jCache.Update())
	MockJobSuccess(mock, j, now, nil, false)
	assert.NoError(t, trybots.jobFinished(j))
	assert.True(t, mock.Empty())

	// Successful job, failed to update.
	err := fmt.Errorf("fail")
	MockJobSuccess(mock, j, now, err, false)
	assert.EqualError(t, trybots.jobFinished(j), err.Error())
	assert.True(t, mock.Empty())

	// Failed job.
	j.Status = db.JOB_STATUS_FAILURE
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j}))
	assert.NoError(t, trybots.jCache.Update())
	MockJobFailure(mock, j, now, nil)
	assert.NoError(t, trybots.jobFinished(j))
	assert.True(t, mock.Empty())

	// Failed job, failed to update.
	MockJobFailure(mock, j, now, err)
	assert.EqualError(t, trybots.jobFinished(j), err.Error())
	assert.True(t, mock.Empty())

	// Mishap.
	j.Status = db.JOB_STATUS_MISHAP
	assert.NoError(t, trybots.db.PutJobs([]*db.Job{j}))
	assert.NoError(t, trybots.jCache.Update())
	MockJobMishap(mock, j, now, nil)
	assert.NoError(t, trybots.jobFinished(j))
	assert.True(t, mock.Empty())

	// Mishap, failed to update.
	MockJobMishap(mock, j, now, err)
	assert.EqualError(t, trybots.jobFinished(j), err.Error())
	assert.True(t, mock.Empty())
}

func TestGetJobToSchedule(t *testing.T) {
	trybots, _, mock, cleanup := setup(t)
	defer cleanup()

	now := time.Now()

	// Normal job, Gerrit patch.
	b1 := Build(t, now)
	MockTryLeaseBuild(mock, b1.Id, now, nil)
	result, err := trybots.getJobToSchedule(b1, now)
	assert.NoError(t, err)
	assert.True(t, mock.Empty())
	assert.Equal(t, result.BuildbucketBuildId, b1.Id)
	assert.Equal(t, result.BuildbucketLeaseKey, b1.LeaseKey)
	assert.True(t, result.Valid())

	// Failed to lease build.
	expectErr := fmt.Errorf("fail")
	MockTryLeaseBuild(mock, b1.Id, now, expectErr)
	result, err = trybots.getJobToSchedule(b1, now)
	assert.EqualError(t, err, expectErr.Error())
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid parameters_json.
	b2 := Build(t, now)
	b2.ParametersJson = "dklsadfklas"
	MockCancelBuild(mock, b2.Id, nil)
	result, err = trybots.getJobToSchedule(b2, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid repo.
	b3 := Build(t, now)
	b3.ParametersJson = testutils.MarshalJSON(t, Params(t, "fake-job", "bogus-repo", "master", gerritPatch.Server, gerritPatch.Issue, gerritPatch.Patchset))
	MockCancelBuild(mock, b3.Id, nil)
	result, err = trybots.getJobToSchedule(b3, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid revision.
	b4 := Build(t, now)
	b4.ParametersJson = testutils.MarshalJSON(t, Params(t, "fake-job", patchProject, "abz", gerritPatch.Server, gerritPatch.Issue, gerritPatch.Patchset))
	MockCancelBuild(mock, b4.Id, nil)
	result, err = trybots.getJobToSchedule(b4, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid patch storage.
	b6 := Build(t, now)
	p := Params(t, "fake-job", patchProject, "master", gerritUrl, gerritPatch.Issue, gerritPatch.Patchset)
	p.Properties.PatchStorage = "???"
	b6.ParametersJson = testutils.MarshalJSON(t, p)
	MockCancelBuild(mock, b6.Id, nil)
	result, err = trybots.getJobToSchedule(b6, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid RepoState.
	b7 := Build(t, now)
	b7.ParametersJson = testutils.MarshalJSON(t, Params(t, "fake-job", patchProject, "bad-revision", gerritPatch.Server, gerritPatch.Issue, gerritPatch.Patchset))
	MockCancelBuild(mock, b7.Id, nil)
	result, err = trybots.getJobToSchedule(b7, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Invalid JobSpec.
	b8 := Build(t, now)
	b8.ParametersJson = testutils.MarshalJSON(t, Params(t, "bogus-job", patchProject, "master", gerritPatch.Server, gerritPatch.Issue, gerritPatch.Patchset))
	MockCancelBuild(mock, b8.Id, nil)
	result, err = trybots.getJobToSchedule(b8, now)
	assert.Nil(t, err) // We don't report errors for bad data from buildbucket.
	assert.Nil(t, result)
	assert.True(t, mock.Empty())

	// Failure to cancel the build.
	b9 := Build(t, now)
	b9.ParametersJson = testutils.MarshalJSON(t, Params(t, "bogus-job", patchProject, "master", gerritPatch.Server, gerritPatch.Issue, gerritPatch.Patchset))
	expect := fmt.Errorf("no cancel!")
	MockCancelBuild(mock, b9.Id, expect)
	result, err = trybots.getJobToSchedule(b9, now)
	assert.EqualError(t, err, expect.Error())
	assert.Nil(t, result)
	assert.True(t, mock.Empty())
}

func TestPoll(t *testing.T) {
	trybots, _, mock, cleanup := setup(t)
	defer cleanup()

	now := time.Now()

	assertAdded := func(builds []*buildbucket_api.ApiBuildMessage) {
		assert.NoError(t, trybots.jCache.Update())
		jobs, err := trybots.jCache.GetActiveTryJobs()
		assert.NoError(t, err)
		byId := make(map[int64]*db.Job, len(jobs))
		for _, j := range jobs {
			// Check that the job creation time is reasonable.
			assert.True(t, j.Created.Year() > 1969 && j.Created.Year() < 3000)
			byId[j.BuildbucketBuildId] = j
			j.Status = db.JOB_STATUS_SUCCESS
			j.Finished = now
		}
		for _, b := range builds {
			_, ok := byId[b.Id]
			assert.True(t, ok)
		}
		assert.NoError(t, trybots.db.PutJobs(jobs))
		assert.NoError(t, trybots.jCache.Update())
	}

	makeBuilds := func(n int) []*buildbucket_api.ApiBuildMessage {
		builds := make([]*buildbucket_api.ApiBuildMessage, 0, n)
		for i := 0; i < n; i++ {
			builds = append(builds, Build(t, now))
		}
		return builds
	}

	mockBuilds := func(builds []*buildbucket_api.ApiBuildMessage) []*buildbucket_api.ApiBuildMessage {
		MockPeek(mock, builds, now, "", "", nil)
		for _, b := range builds {
			MockTryLeaseBuild(mock, b.Id, now, nil)
			MockJobStarted(mock, b.Id, now, nil)
		}
		return builds
	}

	check := func(builds []*buildbucket_api.ApiBuildMessage) {
		assert.Nil(t, trybots.Poll(now))
		assert.True(t, mock.Empty())
		assertAdded(builds)
	}

	// Single new build, success.
	check(mockBuilds(makeBuilds(1)))

	// Multiple new builds, success.
	check(mockBuilds(makeBuilds(5)))

	// More than one page of new builds.
	builds := makeBuilds(PEEK_MAX_BUILDS + 5)
	MockPeek(mock, builds[:PEEK_MAX_BUILDS], now, "", "cursor1", nil)
	MockPeek(mock, builds[PEEK_MAX_BUILDS:], now, "cursor1", "", nil)
	for _, b := range builds {
		MockTryLeaseBuild(mock, b.Id, now, nil)
		MockJobStarted(mock, b.Id, now, nil)
	}
	check(builds)

	// Multiple new builds, fail getJobToSchedule, ensure successful builds
	// are inserted.
	builds = makeBuilds(5)
	failIdx := 2
	failBuild := builds[failIdx]
	failBuild.ParametersJson = "???"
	MockPeek(mock, builds, now, "", "", nil)
	builds = append(builds[:failIdx], builds[failIdx+1:]...)
	for _, b := range builds {
		MockTryLeaseBuild(mock, b.Id, now, nil)
		MockJobStarted(mock, b.Id, now, nil)
	}
	MockCancelBuild(mock, failBuild.Id, nil)
	check(builds)

	// Multiple new builds, fail jobStarted, ensure that the others are
	// properly added.
	builds = makeBuilds(5)
	failBuild = builds[failIdx]
	MockPeek(mock, builds, now, "", "", nil)
	builds = append(builds[:failIdx], builds[failIdx+1:]...)
	for _, b := range builds {
		MockTryLeaseBuild(mock, b.Id, now, nil)
		MockJobStarted(mock, b.Id, now, nil)
	}
	MockTryLeaseBuild(mock, failBuild.Id, now, nil)
	MockJobStarted(mock, failBuild.Id, now, fmt.Errorf("Failed to start build."))
	assert.EqualError(t, trybots.Poll(now), "Got errors loading builds from Buildbucket: [Failed to start build.]")
	assert.True(t, mock.Empty())
	assertAdded(builds)

	// More than one page of new builds, fail peeking a page, ensure that
	// other jobs get added.
	builds = makeBuilds(PEEK_MAX_BUILDS + 5)
	err := fmt.Errorf("Failed peek")
	MockPeek(mock, builds[:PEEK_MAX_BUILDS], now, "", "cursor1", nil)
	MockPeek(mock, builds[PEEK_MAX_BUILDS:], now, "cursor1", "", err)
	builds = builds[:PEEK_MAX_BUILDS]
	for _, b := range builds {
		MockTryLeaseBuild(mock, b.Id, now, nil)
		MockJobStarted(mock, b.Id, now, nil)
	}
	assert.EqualError(t, trybots.Poll(now), "Got errors loading builds from Buildbucket: [Failed peek]")
	assert.True(t, mock.Empty())
	assertAdded(builds)
}
