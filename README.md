# music

Player de música para terminal com busca no YouTube, SoundCloud e biblioteca
local. A interface é construída com Bubble Tea e a reprodução é delegada ao
`mpv`.

## Requisitos

- Go 1.25 ou mais recente, apenas para compilar;
- `mpv`, para reprodução;
- `yt-dlp`, para buscas remotas e downloads;
- FFmpeg, usado pelo `yt-dlp` ao converter downloads para MP3;
- Linux ou outro sistema Unix com sockets Unix e grupos de processos.

No Arch Linux:

```sh
sudo pacman -S go mpv yt-dlp ffmpeg
```

No Linux Mint/Ubuntu:

```sh
sudo apt-get install mpv ffmpeg
```

Instale também Go 1.25 ou mais recente e `yt-dlp`. O Go disponível nos
repositórios de versões antigas do sistema pode ser anterior ao exigido pelo
projeto. Confirme com `go version` e `yt-dlp --version`.

## Compilar e testar

```sh
make test
make build
./bin/music
```

Para disponibilizar o comando `music` no seu usuário:

```sh
make install
music
```

O destino padrão é `~/.local/bin`, que deve estar no `PATH`. Use
`make uninstall` para remover o comando; as músicas e a configuração são
preservadas. Para outro destino, use `make install PREFIX=/caminho`.

Também é possível iniciar uma busca diretamente:

```sh
./bin/music daft punk get lucky
```

Use `./bin/music --help` e `./bin/music --version` para informações rápidas.

## Controles

### Busca

| Tecla/comando | Ação |
| --- | --- |
| `Tab` | alterna entre YouTube, SoundCloud e arquivos locais |
| `Enter` | inicia a busca ou abre a seleção |
| `↑` / `↓` | navega pelo histórico de buscas; no modo local, muda de página |
| `historico` | mostra as faixas reproduzidas recentemente |
| `fila` | resume a faixa atual e a fila |
| `:pasta /caminho` | define a biblioteca local e o destino dos downloads |
| `:buscar termo` | força uma busca; útil para termos numéricos no modo local |
| `:formato mp3` | converte downloads para MP3 |
| `:formato original` | preserva o melhor formato de áudio disponível |
| `:downloads` | mostra progresso e estado das tarefas de download |
| `:download cancelar` | cancela downloads ativos |
| `q` ou `Ctrl+C` | sai |

### Resultados

| Entrada | Ação |
| --- | --- |
| `1` | toca uma faixa |
| `1-5` ou `1,3,7` | adiciona várias faixas à fila |
| `d3` | baixa a faixa 3 |
| `↑` / `↓` | muda de página |
| `Esc` | volta à busca |

Pressionar `Enter` com a seleção vazia escolhe a primeira faixa da página
visível.

### Player e fila

| Tecla/comando | Ação |
| --- | --- |
| `Space` | pausa ou retoma |
| `←` / `→` | retrocede ou avança 10 segundos |
| `↑` / `↓` | altera o volume |
| `S` | liga ou desliga o reforço de graves |
| `p` | volta à busca sem interromper o áudio |
| `q` | pula para a próxima faixa ou para a reprodução |
| `d` | baixa a faixa atual |
| `x` | remove a próxima faixa |
| `c` | limpa a fila |

Na busca ou na seleção também estão disponíveis:

```text
:fila limpar
:fila remover N
:fila mover ORIGEM DESTINO
```

Buscas e carregamentos podem ser cancelados com `Esc`.

O reforço de graves aplica +5 dB em torno de 110 Hz, com compensação e
limitador para reduzir clipping. Ele continua ativo ao avançar para outra faixa
na mesma sessão.

## Arquivos do usuário

O app respeita `XDG_CONFIG_HOME` e `XDG_DATA_HOME` quando definidos:

- configuração: `$XDG_CONFIG_HOME/music/config.json`;
- buscas recentes: `$XDG_DATA_HOME/music/search_history.json`;
- reproduções recentes: `$XDG_DATA_HOME/music/play_history.json`.

Na ausência das variáveis XDG, são usados `~/.config` e
`~/.local/share`. Somente nesse modo padrão, históricos do caminho antigo
`~/.local/share/ytmusic` continuam sendo lidos e são migrados na próxima
gravação. Um `XDG_DATA_HOME` explícito permanece isolado.

Os arquivos JSON são limitados, bloqueados entre processos e escritos de forma
atômica. Cada instância cria um socket privado do `mpv` sob `/tmp`.

## Qualidade

```sh
make fmt-check
make vet
make test
make vuln
```

A suíte cobre parsing e fuzz cases conhecidos, caracteres de controle,
persistência concorrente, busca local, cancelamento de subprocessos, ciclo do
player, mensagens assíncronas, fila, paginação e terminais pequenos.

## Solução de problemas

- **`mpv não encontrado no PATH`**: instale o `mpv` e confirme com
  `mpv --version`.
- **Falha na busca/download**: execute `yt-dlp --version`; o app apresenta o
  erro retornado pela ferramenta. Se uma busca retornar algumas faixas antes de
  falhar, os resultados disponíveis são exibidos com um aviso.
- **Download não converte para MP3**: confirme `ffmpeg -version`.
- **Pasta local inválida**: use um caminho existente com
  `:pasta ~/Music`.

O projeto ainda não declara uma licença pública; isso precisa ser decidido pelo
autor antes da redistribuição.

## Sincronizar com domenyk.com/links

```sh
music --sync-setup https://domenyk-api.vercel.app
```

O comando salva um token aleatório na configuração local (`0600`) e imprime
`MUSIC_DEVICE_TOKEN_SHA256` para cadastrar **no projeto da API** na Vercel.
Repetir o comando mantém o token. Publique a API com a variável configurada e
use esta versão atualizada do player. A integração é opcional: sem
`music_sync` configurado, nenhum evento é enviado.

A faixa é anunciada quando começa a tocar e atualizada a cada 15 segundos.
Pausar, parar e encerrar o player atualizam o estado; um fechamento inesperado
expira em 45 segundos. Cada reprodução com pelo menos 30 segundos efetivamente
ouvidos entra no ranking pessoal; pausas e saltos não contam como escuta.
Falhas de rede aparecem no status e não interrompem a música. O próximo envio
leva o tempo acumulado da faixa atual; não há fila persistente para faixas
encerradas offline. O histórico antigo não é importado como reproduções.

Arquivos locais enviam somente título/metadados, sem o caminho da biblioteca.
A API completa os dados que encontrar no catálogo. O site mostra somente a faixa em reprodução, com capa, título, artista e
um link para buscar o título no YouTube. Não há player de áudio nem lista
de mais ouvidas no card; o histórico continua registrado pela API.

Para desligar o envio neste dispositivo, remova `music_sync` do arquivo
`~/.config/music/config.json` (ou `$XDG_CONFIG_HOME/music/config.json`). Para
revogar o acesso, remova ou substitua o hash na API e faça redeploy.

Se a API estiver atrás da proteção de automação da Vercel, a configuração
`music_sync.protection_bypass` guarda a credencial desse projeto e a envia
somente no header `x-vercel-protection-bypass` para a origem configurada.
Ela fica no mesmo arquivo local privado (`0600`); o setup preserva seu valor.
Essa credencial libera a passagem pela Vercel, mas o token próprio da API
continua obrigatório. Não copie essas credenciais para variáveis públicas.
