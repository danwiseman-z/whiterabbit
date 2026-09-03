# whiterabbit

A small terminal app that estimates how much time you spent on each of your
projects, using your GitHub activity as the timesheet.

A *project* is a name plus a list of GitHub repos. Pick a day, and whiterabbit
shows which projects you touched and a rough estimate of the time you put in.
These are estimates, not billable hours. Below the day's numbers it also lists
what you are meant to be working on: every open issue or pull request assigned
to you that sits in an "In Progress" column on a GitHub Project board.

```
 whiterabbit                                        Wed 2 Sep 2026  (today)

   PROJECT          TIME   ACTIVITY
 > workstation       35m   11 commits
   whiterabbit       20m   1 commit
   ---------------------
   total             55m

   IN PROGRESS  assigned to you on a project board
   whiterabbit   whiterabbit  #12     Show project boards on the day view  · Roadmap
   workstation   kindcluster  PR #40  Pin kind to 0.24  · Roadmap

 h/l day  H/L week  t today  enter detail  r refresh  p projects  a add  q quit
```

## Requirements

- Go 1.27+ to build
- The [`gh` CLI](https://cli.github.com), authenticated (`gh auth login`).
  whiterabbit shells out to `gh api`, so it uses whatever access you already have,
  including private repos.
- To see the in-progress list, the gh token also needs the `read:project`
  scope, which `gh auth login` does not grant by default:

  ```sh
  gh auth refresh -s read:project
  ```

  Without it everything else still works and the section explains what to run.

## Install

```sh
go install github.com/danwiseman-z/whiterabbit@latest
```

Or from a clone:

```sh
go build -o whiterabbit .
```

## Usage

```sh
whiterabbit                      # start on today
whiterabbit -date 2026-08-31     # start on a specific day
whiterabbit -plain               # print today's report and exit
whiterabbit -config ./other.json # use a different config file
```

### Keys

| Key | Where | Action |
| --- | --- | --- |
| `h` / `l` | day | previous / next day |
| `H` / `L` | day | back / forward a week |
| `t` | day | jump to today |
| `enter` | day | show the sessions behind a project's estimate |
| `r` | day | refetch the day and the in-progress list, ignoring the cache |
| `p` | day | manage projects |
| `a` | day, projects | add a project |
| `e` / `d` | projects | edit / delete the selected project |
| `tab` | project form | move between the name and the repo list |
| `b` | project form | browse an account for repos to add |
| `x` | project form | drop the selected repo |
| `ctrl+s` | project form | save |
| `space` | repo browser | select / deselect a repo |
| `ctrl+a` | repo browser | select everything the filter is showing |
| `esc` | anywhere | back |
| `q` | day | quit |

## Adding a project

Press `a`, type a name, then `tab` to the repo list and press `b`. whiterabbit
offers your own account and every organization you belong to; pick one (or type
any other account name) and press `enter` to list its repos, newest push first,
private ones included.

Type to filter the list, `space` to select, `enter` when you are done. Selections
survive going back with `esc` to browse a different account, so one project can
mix repos from your account and an org. If you already know the full name, type
`owner/repo` at the account prompt and `enter` adds it directly. Back on the
form, `x` removes a repo from the list and `ctrl+s` saves.

## Config

Stored at `$XDG_CONFIG_HOME/whiterabbit/config.json`, which is normally
`~/.config/whiterabbit/config.json`. Override with `-config` or
`WHITERABBIT_CONFIG`. You can edit projects in the TUI, or edit the file
directly:

```json
{
  "user": "danwiseman-z",
  "projects": [
    { "name": "workstation", "repos": ["danwiseman-z/dotfiles", "danwiseman-z/kindcluster"] }
  ],
  "settings": {
    "gap_minutes": 45,
    "lead_in_minutes": 20,
    "min_session_minutes": 15,
    "max_session_minutes": 240,
    "in_progress_statuses": ["In Progress"]
  }
}
```

`user` is the GitHub login activity is attributed to; it is filled in from `gh`
on first run. A repo may appear in more than one project.

`in_progress_statuses` names the project board columns that count as in
progress, compared ignoring case, so add `"Doing"` or `"In Review"` if your
boards use those. Set it to `[]` to turn the section off.

## How the estimate works

For the chosen day, whiterabbit asks GitHub for everything you did in each
linked repo:

- commits you authored, on any branch you pushed to (not just the default one)
- issue and pull request comments you wrote
- review comments you left on diffs
- issues and pull requests you opened or closed
- pull requests you merged

Those timestamps are then grouped into sessions:

- events less than `gap_minutes` apart belong to the same sitting
- each sitting gets `lead_in_minutes` credited before its first event, because
  the work happens before the commit that records it
- a sitting is never shorter than `min_session_minutes` or longer than
  `max_session_minutes`, and sittings never overlap

A project's estimate for the day is the sum of its sittings. Press `enter` on a
project to see each session and the events inside it, so you can judge whether
the number is believable.

Branch coverage is worth spelling out, because it is the difference between a
believable number and a blank row: GitHub's commit list walks one branch at a
time and defaults to the default branch. whiterabbit reads the repository
activity feed to find every branch you actually pushed to that day and walks
each one, so a day spent on an unmerged feature branch shows up. Branches
deleted after merging still resolve, because the feed records the commit each
push produced.

## The in-progress list

whiterabbit searches for open issues and pull requests assigned to you, then
reads the [GitHub Projects](https://docs.github.com/issues/planning-and-tracking-with-projects)
boards each one is on and keeps the ones whose Status matches
`in_progress_statuses`. Each line shows the whiterabbit project that tracks the
item's repo (or `-` when none does), the repo, the issue or PR number, its
title, and the board's name. An item on two boards is listed once per board. The list is
fetched once at startup and again on `r`; it is not tied to the selected day.

Draft issues that exist only on a board, with no issue behind them, are not
searchable and so do not appear.

### What it will miss

Work that leaves no trace on GitHub. Long stretches of thinking, reading, or
local work that ends in a single commit look like one short session. Conversely,
a day of merging bot PRs looks like real work. Commits you authored but somebody
else pushed are only found once they reach the default branch. Tune `gap_minutes` and
`lead_in_minutes` to taste.
