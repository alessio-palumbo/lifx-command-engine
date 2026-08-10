# Third-party notices

The MIT license in this repository covers the original `lifx-command-engine`
source code. It does not relicense third-party software or model weights.

## FunctionGemma

FunctionGemma model weights are not included in this repository. The optional
installer downloads them directly from Kaggle only when requested by the user.
FunctionGemma is provided under the Gemma Terms of Use:

https://ai.google.dev/gemma/terms

Anyone downloading, using, modifying, hosting, or redistributing FunctionGemma
or a model derivative is responsible for complying with those terms and the
incorporated prohibited-use policy. Distribution of Gemma weights or model
derivatives has additional notice and downstream-terms requirements. The MIT
license for this engine does not replace or modify those requirements.

## Optional runtimes and Go dependencies

whisper.cpp, Transformers, PyTorch, KaggleHub, and the Go modules listed in
`go.mod` remain subject to their respective licenses. They are not relicensed
by this repository. Model weights and optional runtime executables are not
included in normal engine builds.
