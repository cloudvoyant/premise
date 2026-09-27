# Questionnaire Module

## Purpose

The questionnaire module collects interactive answers for template selection and generation. It adapts terminal prompts to the data required by registry and generate workflows.

## Design

### API

| API                                       | Use                                                       |
| ----------------------------------------- | --------------------------------------------------------- |
| `Questionnaire`                           | Abstract answer collection for generation.                |
| `InteractiveQuestionnaire`                | Terminal implementation backed by `huh`.                  |
| `InteractiveQuestionnaire.Ask(questions)` | Collect and validate answers to template questions.       |
| `AskTemplateKind()`                       | Select `app` or `lib` while initializing a template.      |
| `AskDefaultTemplate(ctx)`                 | Select a qualified template from all official registries. |
| `AskRegistryTemplate(ctx, source)`        | Select a template from one registry source.               |

### Usage

```go
options := core.GenerateOptions{
    Questionnaire: core.InteractiveQuestionnaire{},
}
err := core.Generate(ctx, cwd, selector, options, output)
```

Custom callers can implement `Questionnaire` to provide deterministic, non-interactive answers.

## Implementation Details

The interactive implementation builds terminal fields from registry `Question` declarations, validates required answers, and returns a map keyed by question name. Template-selection prompts load entries through the registry module and return qualified selectors rather than anonymous display labels.

The module owns interaction only. It does not resolve template source paths, apply substitutions, or write files. Those responsibilities belong to [Registry](registry.md) and [Generate](generate.md).
