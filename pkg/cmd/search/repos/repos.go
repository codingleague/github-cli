package repos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cli/cli/v2/internal/config"
	"github.com/cli/cli/v2/pkg/cmd/search/shared"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/export"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/pkg/search"
	"github.com/cli/cli/v2/pkg/text"
	"github.com/cli/cli/v2/utils"
	"github.com/spf13/cobra"
)

type ReposOptions struct {
	Browser      cmdutil.Browser
	Config       func() (config.Config, error)
	Exporter     cmdutil.Exporter
	GoTemplate   string
	HttpClient   func() (*http.Client, error)
	IO           *iostreams.IOStreams
	JqExpression string
	Query        search.Query
	WebMode      bool
}

func NewCmdRepos(f *cmdutil.Factory, runF func(*ReposOptions) error) *cobra.Command {
	opts := &ReposOptions{
		Browser:    f.Browser,
		Config:     f.Config,
		HttpClient: f.HttpClient,
		IO:         f.IOStreams,
		Query:      NewSearchQuery(),
	}

	cmd := &cobra.Command{
		Use:   "repos [<query>]",
		Short: "Search repositories",
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query.Keywords = args
			err := cmdutil.MutuallyExclusive("expected exactly one of `--jq`, `--template`, or `--web`",
				opts.GoTemplate != "",
				opts.JqExpression != "",
				opts.WebMode)
			if err != nil {
				return err
			}
			if opts.Query.Limit < 1 || opts.Query.Limit > 1000 {
				return cmdutil.FlagErrorf("`--limit` must be between 1 and 1000")
			}
			if runF != nil {
				return runF(opts)
			}
			return reposRun(opts)
		},
	}

	// Output flags
	cmd.Flags().StringVarP(&opts.GoTemplate, "template", "t", "", "Format JSON output using a Go template")
	cmd.Flags().StringVarP(&opts.JqExpression, "jq", "q", "", "Format JSON output using a jq `expression`")
	cmd.Flags().BoolVarP(&opts.WebMode, "web", "w", false, "Open the query in the web browser")

	// Query parameter flags
	cmd.Flags().IntVarP(&opts.Query.Limit, "limit", "L", 30, "Maximum number of repositories to fetch")
	cmd.Flags().Var(opts.Query.Order, "order", "Order of repositories returned, ignored unless '--sort' is specified")
	cmd.Flags().Var(opts.Query.Sort, "sort", "Sorts the repositories by stars, forks, help-wanted-issues, or updated")

	// Query qualifier flags
	cmd.Flags().Var(opts.Query.Qualifiers["Archived"], "archived", "Filter based on archive state")
	cmd.Flags().Var(opts.Query.Qualifiers["Created"], "created", "Filter based on created at date")
	cmd.Flags().Var(opts.Query.Qualifiers["Followers"], "followers", "Filter based on number of followers")
	cmd.Flags().Var(opts.Query.Qualifiers["Fork"], "include-forks", "Include forks in search")
	cmd.Flags().Var(opts.Query.Qualifiers["Forks"], "forks", "Filter on number of forks")
	cmd.Flags().Var(opts.Query.Qualifiers["GoodFirstIssues"], "good-first-issues", "Filter on number of issues with the 'good first issue' label")
	cmd.Flags().Var(opts.Query.Qualifiers["HelpWantedIssues"], "help-wanted-issues", "Filter on number of issues with the 'help wanted' label")
	cmd.Flags().Var(opts.Query.Qualifiers["In"], "in", "Restrict search to the name, description, or README file")
	cmd.Flags().Var(opts.Query.Qualifiers["Language"], "language", "Filter based on the coding language")
	cmd.Flags().Var(opts.Query.Qualifiers["License"], "license", "Filter based on license type")
	cmd.Flags().Var(opts.Query.Qualifiers["Mirror"], "mirror", "Filter based on mirror state")
	cmd.Flags().Var(opts.Query.Qualifiers["Org"], "org", "Filter on organization")
	cmd.Flags().Var(opts.Query.Qualifiers["Pushed"], "updated", "Filter on last updated at date")
	cmd.Flags().Var(opts.Query.Qualifiers["Repo"], "repo", "Filter on repository name")
	cmd.Flags().Var(opts.Query.Qualifiers["Size"], "size", "Filter on a size range, in kilobytes")
	cmd.Flags().Var(opts.Query.Qualifiers["Stars"], "stars", "Filter on number of stars")
	cmd.Flags().Var(opts.Query.Qualifiers["Topic"], "topic", "Filter on topic")
	cmd.Flags().Var(opts.Query.Qualifiers["Topics"], "number-topics", "Filter on number of topics")
	cmd.Flags().Var(opts.Query.Qualifiers["User"], "user", "Filter based on user")
	cmd.Flags().Var(opts.Query.Qualifiers["Visibility"], "visibility", "Filter based on visibility")

	return cmd
}

func reposRun(opts *ReposOptions) error {
	io := opts.IO
	cfg, err := opts.Config()
	if err != nil {
		return err
	}
	host, err := cfg.DefaultHost()
	if err != nil {
		return err
	}
	client, err := opts.HttpClient()
	if err != nil {
		return err
	}
	searcher := shared.NewSearcher(client, host)
	if opts.WebMode {
		url := searcher.URL(opts.Query)
		if io.IsStdoutTTY() {
			fmt.Fprintf(io.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(url))
		}
		return opts.Browser.Browse(url)
	}
	opts.IO.StartProgressIndicator()
	result, err := searcher.Repositories(opts.Query)
	opts.IO.StopProgressIndicator()
	if err != nil {
		return err
	}
	if err := opts.IO.StartPager(); err == nil {
		defer opts.IO.StopPager()
	} else {
		fmt.Fprintf(opts.IO.ErrOut, "failed to start pager: %v\n", err)
	}
	if opts.JqExpression != "" {
		j, err := json.Marshal(result.Items)
		if err != nil {
			return err
		}
		err = export.FilterJSON(io.Out, bytes.NewReader(j), opts.JqExpression)
		if err != nil {
			return err
		}
	} else if opts.GoTemplate != "" {
		t := export.NewTemplate(opts.IO, opts.GoTemplate)
		j, err := json.Marshal(result.Items)
		if err != nil {
			return err
		}
		err = t.Execute(bytes.NewReader(j))
		if err != nil {
			return err
		}
	} else {
		err := displayResults(opts.IO, result)
		if err != nil {
			return err
		}
	}
	return nil
}

func displayResults(io *iostreams.IOStreams, results search.RepositoriesResult) error {
	cs := io.ColorScheme()
	tp := utils.NewTablePrinter(io)
	for _, repo := range results.Items {
		var tags []string
		if repo.Private {
			tags = append(tags, "private")
		} else {
			tags = append(tags, "public")
		}
		if repo.Fork {
			tags = append(tags, "fork")
		}
		if repo.Archived {
			tags = append(tags, "archived")
		}
		info := strings.Join(tags, ", ")
		infoColor := cs.Gray
		if repo.Private {
			infoColor = cs.Yellow
		}
		tp.AddField(repo.FullName, nil, cs.Bold)
		description := repo.Description
		tp.AddField(text.ReplaceExcessiveWhitespace(description), nil, nil)
		tp.AddField(info, nil, infoColor)
		if tp.IsTTY() {
			tp.AddField(repo.UpdatedAt.Format(time.RFC822), nil, cs.Gray)
		} else {
			tp.AddField(repo.UpdatedAt.String(), nil, nil)
		}
		tp.EndRow()
	}

	if io.IsStdoutTTY() {
		header := "No repositories matched your search\n"
		if len(results.Items) > 0 {
			header = fmt.Sprintf("Showing %d of %d repositories\n\n", len(results.Items), results.Total)
		}
		fmt.Fprintf(io.Out, "\n%s", header)
	}

	return tp.Render()
}

func NewSearchQuery() search.Query {
	return search.Query{
		Kind:  "repositories",
		Order: shared.NewParameter("order", "string", "desc", shared.OptsValidator([]string{"asc", "desc"})),
		Sort:  shared.NewParameter("sort", "string", "best match", shared.OptsValidator([]string{"forks", "help-wanted-issues", "stars", "updated"})),
		Qualifiers: search.Qualifiers{
			"Archived":         shared.NewQualifier("archived", "bool", "", shared.BoolValidator()),
			"Created":          shared.NewQualifier("created", "string", "", shared.DateValidator()),
			"Followers":        shared.NewQualifier("followers", "string", "", shared.RangeValidator()),
			"Fork":             shared.NewQualifier("fork", "string", "false", shared.OptsValidator([]string{"false", "true", "only"})),
			"Forks":            shared.NewQualifier("forks", "string", "", shared.RangeValidator()),
			"GoodFirstIssues":  shared.NewQualifier("good-first-issues", "string", "", shared.RangeValidator()),
			"HelpWantedIssues": shared.NewQualifier("help-wanted-issues", "string", "", shared.RangeValidator()),
			"In":               shared.NewQualifier("in", "stringSlice", "name, description", shared.MultiOptsValidator([]string{"name", "description", "readme"})),
			"Language":         shared.NewQualifier("language", "stringSlice", "", nil),
			"License":          shared.NewQualifier("license", "stringSlice", "", nil),
			"Mirror":           shared.NewQualifier("mirror", "bool", "", shared.BoolValidator()),
			"Org":              shared.NewQualifier("org", "stringSlice", "", nil),
			"Pushed":           shared.NewQualifier("pushed", "string", "", shared.DateValidator()),
			"Repo":             shared.NewQualifier("repo", "stringSlice", "", nil),
			"Size":             shared.NewQualifier("size", "string", "", shared.RangeValidator()),
			"Stars":            shared.NewQualifier("stars", "string", "", shared.RangeValidator()),
			"Topic":            shared.NewQualifier("topic", "stringSlice", "", nil),
			"Topics":           shared.NewQualifier("topics", "string", "", shared.RangeValidator()),
			"User":             shared.NewQualifier("user", "stringSlice", "", nil),
			"Visibility":       shared.NewQualifier("is", "string", "all", shared.OptsValidator([]string{"public", "private"})),
		},
	}
}
