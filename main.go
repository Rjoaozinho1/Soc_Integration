package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/beevik/etree"
	_ "github.com/lib/pq"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

func main() {
	// Inicia a goroutine para rodar o workDatabase em paralelo
	go workDatabase()
	// Configuração das rotas do servidor
	http.HandleFunc("/api/v1/agendamento", handleAgendamento)
	http.HandleFunc("/api/v1/empresa", handleGetCnpjs)
	http.HandleFunc("/api/v1/setor", handleGetSetores)
	http.HandleFunc("/api/v1/cargo", handleGetCargos)
	http.HandleFunc("/api/v1/funcionario", handleGetCpfs)
	http.HandleFunc("/api/v1/registrar", handleCriaFuncionario)
	// Inicia o servidor HTTP
	log.Println("Servidor iniciado na porta 2026...")
	if err := http.ListenAndServe(":2026", nil); err != nil {
		// deu errado
		log.Printf("Erro ao iniciar o servidor: %v", err)
	}
}

// handler de criar funcionario
func handleCriaFuncionario(w http.ResponseWriter, r *http.Request) {
	// verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	// pega as variaveis necessario para a requisição dentro do body
	// codigoCargo, nomeCargo, codigoEmpresa, cpf, dataNascimento, nomeFuncionario, codigoSetor, nomeSetor
	bodyReq := r.Body
	body, err := io.ReadAll(bodyReq)
	if err != nil {
		log.Printf("erro ao ler o corpo da requisição: %v", err)
		http.Error(w, "erro ao ler o corpo da requisição", http.StatusInternalServerError)
		return
	}
	var funcionario FuncionarioReq
	err = json.Unmarshal(body, &funcionario)
	if err != nil {
		log.Printf("erro ao trasnformar o body em variavel: %v", err)
		http.Error(w, "erro ao trasnformar o body em variavel", http.StatusInternalServerError)
		return
	}
	if funcionario.Pis == "" {
		log.Printf("erro ao criar o funcionario, pis inexistente")
		http.Error(w, "erro ao criar o funcionario, pis inexistente", http.StatusBadRequest)
		return
	}
	// criar o agendamento com os parametros da requisição
	matriculaNova, err := createFuncionario(funcionario.CodigoCargo, funcionario.NomeCargo, funcionario.CodigoEmpresa, funcionario.CPF, funcionario.DataNascimento, funcionario.NomeFuncionario, funcionario.CodigoSetor, funcionario.NomeSetor, funcionario.RG, funcionario.Telefone, funcionario.NomeEmpresa, funcionario.CNPJEmpresa, funcionario.Pis)
	if err != nil {
		log.Printf("erro ao criar o funcionario")
		http.Error(w, "erro ao criar o funcionario", http.StatusBadRequest)
		return
	}
	if matriculaNova == "" {
		log.Printf("matricula do funcionario nao encontrada")
		http.Error(w, "matricula do funcionario nao encontrada", http.StatusBadRequest)
		return
	}
	// retornar se encontrou e se nao encontrou
	w.Header().Set("Content-Type", "application/json")
	// transforma todo o funcionario em json e retorna
	err = json.NewEncoder(w).Encode(matriculaNova)
	if err != nil {
		log.Printf("Erro ao retornar o cnpj desejado: %v", err)
		http.Error(w, `{"message": "Erro ao retornar o cnpj desejado"}`, http.StatusInternalServerError)
		return
	}
}

// handler do endpoint de agendamento para agendar, verificar data-hora,
func handleAgendamento(w http.ResponseWriter, r *http.Request) {
	// verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case "POST": // cria agendamento
		// tratar datas desta forma - dd/mm/aaaa
		dataParam := r.URL.Query().Get("data")
		hourParam := r.URL.Query().Get("hora")
		compromisso := r.URL.Query().Get("compromisso")
		empresa := r.URL.Query().Get("empresa")
		codigoFuncionario := r.URL.Query().Get("matricula")
		// verificar se alguum parametros esta faltando
		if !r.URL.Query().Has("data") || !r.URL.Query().Has("hora") || !r.URL.Query().Has("compromisso") || !r.URL.Query().Has("empresa") {
			log.Printf("faltando parametros necessarios")
			http.Error(w, "faltando parametros necessarios", http.StatusBadRequest)
			return
		}
		supostoDiaAgend, err := time.Parse("02/01/2006", dataParam)
		if err != nil {
			log.Printf("Formato de data inválido: %v\n", err)
			http.Error(w, "Formato de data inválido. Use o formato dd/mm/yyyy.", http.StatusBadRequest)
			return
		}
		var codigoAgenda string
		// Verificar se a data fornecida é antes de 31 de dezembro de 2024
		dateLimit := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
		if supostoDiaAgend.Before(dateLimit) {
			codigoAgenda = "27295"
		} else {
			codigoAgenda = "3015983"
		}
		// criar o agendamento com os parametros da requisição
		err = createAgendamento(dataParam, hourParam, compromisso, empresa, codigoFuncionario, codigoAgenda)
		if err != nil {
			log.Printf("erro ao criar agendamento")
			http.Error(w, "erro ao criar agendamento", http.StatusBadRequest)
			return
		}

	case "GET": // busca agendamentos
		// tratar datas desta forma - dd/mm/aaaa
		dataParam := r.URL.Query().Get("data")
		if dataParam == "" {
			log.Println("data nao preenchido")
			http.Error(w, "data nao preenchido", http.StatusBadRequest)
			return
		}
		// se a hora existe é para verificar se esse horario esta disponivel
		hourParam := r.URL.Query().Get("hora")
		// varaivel para erro global
		var err error
		// formatar a data escolhida para agendamento como dd/mm/aaaa
		supostoDiaAgend, err := time.Parse("02/01/2006", dataParam)
		if err != nil {
			log.Printf("Formato de data inválido: %v\n", err)
			http.Error(w, "Formato de data inválido. Use o formato dd/mm/yyyy.", http.StatusBadRequest)
			return
		}
		// verificar se o suposto supostoDiaAgend é um fim de semana
		if supostoDiaAgend.Weekday() == time.Saturday || supostoDiaAgend.Weekday() == time.Sunday {
			// fim de semana aqui
			log.Println("Dia informado é final de semana")
			http.Error(w, "Dia informado é final de semana", http.StatusBadRequest)
			return
		}
		// Definir o início do período da pesquisa de agendamentos
		diaInicio := strings.Split(dataParam, "/")[0] //fmt.Sprintf("%02d", now.Day())
		mesInicio := strings.Split(dataParam, "/")[1] //fmt.Sprintf("%02d", now.Month())
		anoInicio := strings.Split(dataParam, "/")[2] //fmt.Sprintf("%d", now.Year())
		// now = now.AddDate(0, 0, 30) // Adiciona 30 dias ao início
		diaFim := strings.Split(dataParam, "/")[0] //fmt.Sprintf("%02d", now.Day())
		mesFim := strings.Split(dataParam, "/")[1] //fmt.Sprintf("%02d", now.Month())
		anoFim := strings.Split(dataParam, "/")[2] //fmt.Sprintf("%d", now.Year())
		// trazer todos os agendamentos do mes atual
		log.Println("data inicio:", diaInicio, mesInicio, anoInicio)
		log.Println("data fim", diaFim, mesFim, anoFim)
		agendamentoResponse, err := getAgendamento(diaInicio, mesInicio, anoInicio, diaFim, mesFim, anoFim)
		if err != nil {
			log.Println("Erro ao buscar os agendamentos no SOC:", err)
			http.Error(w, "Erro ao buscar os agendamentos no SOC", http.StatusInternalServerError)
			return
		}
		horariosAgendaProteger, err := getAgendaProteger(diaInicio, mesInicio, anoInicio, diaFim, mesFim, anoFim)
		if err != nil {
			log.Println("Erro ao buscar os agendamentos da Agenda Proteger no SOC:", err)
			http.Error(w, "Erro ao buscar os agendamentos da Agenda Proteger no SOC", http.StatusInternalServerError)
			return
		}
		// inicialização do slice de horarios
		var agendamentosLivres []Horario
		// coloca os horarios de datas dentro do slice
		err = json.Unmarshal(agendamentoResponse, &agendamentosLivres)
		if err != nil {
			log.Printf("Erro ao montar corpo da resposta SOC: %v", err)
			http.Error(w, "Erro ao montar corpo da resposta SOC", http.StatusNotAcceptable)
			return
		}
		// inicialização do slice de horarios da agenda proteger
		var agendamentosLivresAgendaProteger []Horario
		// coloca os horarios de datas dentro do slice
		err = json.Unmarshal(horariosAgendaProteger, &agendamentosLivresAgendaProteger)
		if err != nil {
			log.Printf("Erro ao montar corpo da resposta SOC com agenda Proteger: %v", err)
			http.Error(w, "Erro ao montar corpo da resposta SOC com agenda Proteger", http.StatusNotAcceptable)
			return
		}
		// procurar pelos horarios ocupados o supostoDiaAgend
		diaAgendamento := supostoDiaAgend.Format("02/01/2006")
		log.Println("data agendamento:", diaAgendamento)
		// seta a location para o fuso de brasilia
		location := time.FixedZone("GMT-3", -3*60*60)
		// carrega a localização do brasil para o now
		now := time.Now().In(location)
		// Formatar como dd/mm/yyyy para comparar com diaAgendamento
		hoje := now.Format("02/01/2006")
		if supostoDiaAgend.Before(now.Truncate(24 * time.Hour)) {
			log.Println(diaAgendamento)
			log.Println(now.Truncate(24 * time.Hour))
			log.Println("dia informado é invalido -", diaAgendamento)
			http.Error(w, "dia invalido", http.StatusBadRequest)
			return
		}
		// verificar se o dia informado é um feriado
		if isHoliday(supostoDiaAgend) {
			log.Println("não é possível agendar em feriados - ", diaAgendamento)
			http.Error(w, "não é possível agendar em feriados", http.StatusBadRequest)
			return
		}
		// agenda clientes
		// cria um map para armazenar os horarios que estao livres
		horariosLivres := make(map[string]int)
		horariosLivresAgendaProteger := make(map[string]int)
		// itera sobre todos os horarios do slice
		for _, dataHora := range agendamentosLivres {
			// transformar o dataHora.Data em time.Time
			dia, err := time.Parse("02/01/2006", dataHora.Data)
			if err != nil {
				log.Printf("Formato de data inválido: %v\n", err)
				http.Error(w, "Formato de data inválido. Use o formato dd/mm/yyyy.", http.StatusBadRequest)
				return
			}
			// verifica se tem agendamento para o dia do agendamento
			if supostoDiaAgend.Equal(dia) {
				// quantidade de agendamentos para cada agendamento
				horariosLivres[dataHora.Horario]++
			}
		}
		for _, dataHora := range agendamentosLivresAgendaProteger {
			// transformar o dataHora.Data em time.Time
			dia, err := time.Parse("02/01/2006", dataHora.Data)
			if err != nil {
				log.Printf("Formato de data inválido: %v\n", err)
				http.Error(w, "Formato de data inválido. Use o formato dd/mm/yyyy.", http.StatusBadRequest)
				return
			}
			// verifica se tem agendamento para o dia do agendamento
			if supostoDiaAgend.Equal(dia) {
				// quantidade de agendamentos para cada agendamento
				horariosLivresAgendaProteger[dataHora.Horario]++
			}
		}
		// precisa pegar o que esta entre cada horario e adicionar ao map
		log.Println("map horarios livres:", horariosLivres)
		log.Println("map horarios livres agenda proteger:", horariosLivresAgendaProteger)
		// Horas que serao feitas os agendamentos
		horariosTrabalho := []string{"07:30", "08:00", "08:30", "09:00", "09:30", "10:00", "10:30", "11:00", "11:30", "12:00", "12:30", "13:00", "13:30", "14:00", "14:30", "15:00", "15:30", "16:00", "16:30"}
		// verificat quantos atendimentos ja estao marcados em cada agenda
		novaListaHorariosLivres := make(map[string]int)
		// verificar o horario para diferenciar a qtd de marcações
		novaListaHorariosLivres[diaAgendamento] = 0
		for _, horarioTrabalho := range horariosTrabalho {
			if horariosLivresAgendaProteger[horarioTrabalho] == 3 {
				// 2 horarios livre
				novaListaHorariosLivres[diaAgendamento] += 1
				continue
			} else if horariosLivresAgendaProteger[horarioTrabalho] == 2 {
				// 1 horarios livre
				novaListaHorariosLivres[diaAgendamento] += 1
				continue
			} else {
				// 0 horarios livre
				continue
			}
		}
		log.Println("Lista horarios livres Agenda Proteger:", novaListaHorariosLivres)

		novaListaHorariosLivresAgendaClientes := make(map[string]int)
		// verificar o horario para diferenciar a qtd de marcações
		novaListaHorariosLivresAgendaClientes[diaAgendamento] = 0
		for _, horarioTrabalho := range horariosTrabalho {
			if horariosLivres[horarioTrabalho] > 5 {
				horariosLivres[horarioTrabalho] = 5
			}
			// log.Printf("horario %s = %d", horarioTrabalho, horariosLivres[horarioTrabalho])
			if horariosLivres[horarioTrabalho] == 5 {
				// 5 horarios livre
				novaListaHorariosLivresAgendaClientes[diaAgendamento] += 5
				continue
			} else if horariosLivres[horarioTrabalho] == 4 {
				// 4 horarios livre
				novaListaHorariosLivresAgendaClientes[diaAgendamento] += 4
				continue
			} else if horariosLivres[horarioTrabalho] == 3 {
				// 3 horarios livre
				novaListaHorariosLivresAgendaClientes[diaAgendamento] += 3
				continue
			} else if horariosLivres[horarioTrabalho] == 2 {
				// 2 horarios livre
				novaListaHorariosLivresAgendaClientes[diaAgendamento] += 2
				continue
			} else if horariosLivres[horarioTrabalho] == 1 {
				// 1 horarios livre
				novaListaHorariosLivresAgendaClientes[diaAgendamento] += 1
				continue
			} else {
				// 0 horarios livre
				continue
			}
		}
		log.Println("Lista horarios livres Agenda Clientes:", novaListaHorariosLivresAgendaClientes)
		// Cria slice para armazenar os horários disponíveis
		var horariosDisponiveis []Horario
		// verifica se existe o parametro de horario
		if hourParam != "" {
			// Verifica se o horário específico está disponível no dia fornecido
			if horariosLivres[hourParam] > 0 && (horariosLivresAgendaProteger[hourParam] == 2 || horariosLivresAgendaProteger[hourParam] == 3) {
				// horario esta disponivel
				log.Println("Horario Disponivel")
				w.Write([]byte("Horario Disponivel"))
				return
			} else {
				// horario nao disponivel
				log.Println("Horario não esta disponivel")
				http.Error(w, "false", http.StatusConflict)
				return
			}
		} else {
			// itera sobre cada horario de trabalho
			log.Println("Dia Agendamento:", diaAgendamento)
			log.Println("Hoje           :", hoje)
			for _, horario := range horariosTrabalho {
				// verifica se é o dia do agendamento é hoje e se o hario ja passou
				if diaAgendamento == hoje && horario <= now.Format("15:04") {
					// Verificar se o horário já passou
					log.Printf("Horário %s já passou, pulando...\n", horario)
					continue // Pula o horário que já passou
				}
				// Verifica dentro do map se o horario nao possui agendamentos
				if horariosLivres[horario] > 0 && (horariosLivresAgendaProteger[horario] == 2 || horariosLivresAgendaProteger[horario] == 3) {
					log.Println("horarios disponivel:", horario)
					// adiciona o horario para o slice de horarios disponiveis
					horariosDisponiveis = append(horariosDisponiveis, Horario{
						Data:    diaAgendamento,
						Horario: horario,
					})
				}
			}
		}
		// Responder com os horários disponíveis
		w.Header().Set("Content-Type", "application/json")
		// transforma todo o array de horarios em json
		err = json.NewEncoder(w).Encode(horariosDisponiveis)
		if err != nil {
			log.Printf("Erro ao retornar horários: %v", err)
			http.Error(w, "Erro ao retornar horários", http.StatusInternalServerError)
			return
		}
		//}
	default:
		log.Println("metodo nao suportado")
		http.Error(w, "metodo nao suportado", http.StatusMethodNotAllowed)
		return
	}
}

// handler do endpoint de cnpj para buscar cnpj da empresa
func handleGetCnpjs(w http.ResponseWriter, r *http.Request) {
	// verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, `{"message": "não autorizado", "data": ""}`, http.StatusUnauthorized)
		return
	}
	// pegar o cnpj dos parametros
	q := r.URL.Query()
	cnpj := q.Get("cnpj")
	cnpj = func(cnpj string) string { re := regexp.MustCompile(`[^\d]`); return re.ReplaceAllString(cnpj, "") }(cnpj)
	// verificar se o cnpj esta no formato correto com 14 de length
	if len(cnpj) > 14 {
		log.Println("cnpj nao valido")
		http.Error(w, `{"message": "cnpj nao valido", "data": ""}`, http.StatusBadRequest)
		return
	}
	// pesquisar na funcao de fetchProductByCnpj com o cnpj formatado
	empresa, err := fetchProductByCnpj(cnpj)
	if err != nil {
		log.Printf("erro ao trazer cnpj com o valor recebido: %v", err)
		http.Error(w, `{"message":"erro ao trazer cnpj com o valor recebido", "data": ""}`, http.StatusNotFound)
		return
	}
	// retornar se encontrou e se nao encontrou
	w.Header().Set("Content-Type", "application/json")
	// transforma todo o funcionario em json e retorna
	err = json.NewEncoder(w).Encode(empresa)
	if err != nil {
		log.Printf("Erro ao retornar o cnpj desejado: %v", err)
		http.Error(w, `{"message": "Erro ao retornar o cnpj desejado"}`, http.StatusInternalServerError)
		return
	}
}

// handler para pequisar os cpf dentro do SOC
func handleGetCpfs(w http.ResponseWriter, r *http.Request) {
	// verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, `{"message": "não autorizado"}`, http.StatusUnauthorized)
		return
	}
	// verificar o parametro do cpf da requisição
	cpf := r.URL.Query().Get("cpf")
	empresa := r.URL.Query().Get("empresa")
	if !r.URL.Query().Has("cpf") || !r.URL.Query().Has("empresa") {
		log.Printf("faltando parametros necessarios")
		http.Error(w, `{"message": "faltando parametros necessarios"}`, http.StatusBadRequest)
		return
	}
	//formatar o cpf para somente numeros
	cpf = func(cpf string) string {
		cpf = strings.ReplaceAll(cpf, ".", "")
		cpf = strings.ReplaceAll(cpf, "-", "")
		return cpf
	}(cpf)
	// procurar o cpf no SOC
	body, err := getCpfSoc(empresa, cpf)
	if err != nil {
		log.Println("Erro ao buscar cpf dentro do Soc:", err)
		http.Error(w, "Erro ao buscar cpf dentro do Soc", http.StatusInternalServerError)
		return
	}
	//	log.Println(string(body))
	var funci []Funcionario
	err = json.Unmarshal(body, &funci)
	if err != nil {
		log.Println("erro ao transformar a resposta em json")
		http.Error(w, "erro ao transformar a resposta em json", http.StatusInternalServerError)
		return
	}
	// retornar se encontrou e se nao encontrou
	w.Header().Set("Content-Type", "application/json")
	// transforma todo o funcionario em json e retorna
	err = json.NewEncoder(w).Encode(funci)
	if err != nil {
		log.Printf("Erro ao retornar o cpf desejado: %v", err)
		http.Error(w, "Erro ao retornar o cpf desejado", http.StatusInternalServerError)
		return
	}
}

// handler do endpoint de cargos
func handleGetCargos(w http.ResponseWriter, r *http.Request) {
	// Verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	// Pegar o ID da empresa e o setor nos parâmetros
	empresa := r.URL.Query().Get("empresa")
	setor := r.URL.Query().Get("setor")
	if empresa == "" || setor == "" {
		log.Println("empresa ou setor nao preenchido")
		http.Error(w, "empresa ou setor nao preenchido", http.StatusBadRequest)
		return
	}
	// Buscar hierarquia no endpoint SOC
	unit, err := fetchHierarquia(empresa)
	if err != nil {
		log.Printf("erro ao trazer hierarquia de setores: %v", err)
		http.Error(w, `{"message": "erro ao trazer hierarquia de setores"}`, http.StatusNotFound)
		return
	}
	// Filtrar cargos ativos do setor específico
	var cargosAtivos []CargoResponse
	for _, unidade := range unit {
		if unidade.NomeSetor == setor && unidade.AtivoSetor == "Sim" && unidade.AtivoCargo == "Sim" {
			cargoUTF8, err := decodeToUTF8([]byte(unidade.NomeCargo))
			if err != nil {
				log.Printf("Erro ao converter para UTF-8: %v\n", err)
				continue
			}
			cargosAtivos = append(cargosAtivos, CargoResponse{
				ID:    unidade.CodigoCargo,
				Cargo: cargoUTF8,
			})
		}
	}
	// Retornar cargos ativos do setor
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(cargosAtivos)
	if err != nil {
		log.Printf("Erro ao retornar cargos: %v", err)
		http.Error(w, "Erro ao retornar cargos", http.StatusInternalServerError)
		return
	}
}

// handler do endpoint de setores
func handleGetSetores(w http.ResponseWriter, r *http.Request) {
	// Verificar autenticação
	if r.Header.Get("Authorization") != "TOKEN" {
		log.Println("não autorizado")
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	// Pegar o ID da empresa nos parâmetros
	empresa := r.URL.Query().Get("empresa")
	if empresa == "" {
		log.Println("empresa nao preenchido")
		http.Error(w, "empresa nao preenchido", http.StatusBadRequest)
		return
	}
	// Buscar setores no endpoint SOC
	setores, err := fetchSetorSOC()
	if err != nil {
		log.Printf("erro ao trazer setores: %v", err)
		http.Error(w, "erro ao trazer setores", http.StatusNotFound)
		return
	}
	// Filtrar setores da empresa específica e converter para array de objetos
	var setoresEmpresa []SetorResponse
	// Filtrar setores da empresa específica
	for _, v := range setores {
		if v.CodigoEmpresa == empresa && v.SetorAtivo == "1" {
			setorUTF8, err := decodeToUTF8([]byte(v.NomeSetor))
			if err != nil {
				log.Printf("Erro ao converter para UTF-8: %v\n", err)
				continue
			}
			setoresEmpresa = append(setoresEmpresa, SetorResponse{
				ID:    v.CodigoSetor,
				Setor: setorUTF8,
			})
		}
	}
	// Retornar setores ativos
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(setoresEmpresa)
	if err != nil {
		log.Printf("Erro ao retornar setores: %v", err)
		http.Error(w, "Erro ao retornar setores", http.StatusInternalServerError)
		return
	}
}

// funcao para rodar o populador do banco
func workDatabase() {
	// conexao com banco
	connStr := "postgresstringconnection"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Println(err)
	}
	// fechar conexao apos execução
	defer db.Close()

	for {
		// funcao de testar conexao
		if err = db.Ping(); err != nil {
			log.Println(err)
		}
		// criar a tabela se ja nao existe
		createProductTable(db)

		// sincronizar dados da API e banco
		syncDataWithAPI(db)

		time.Sleep(20 * time.Hour)
	}
}

// funcao de popular o banco com os dados do SOC
func syncDataWithAPI(db *sql.DB) {
	// verificar se os dados da api estao iguais
	empSoc := getEmpresas()
	// verificar os dados da tabela
	empDb := fetchEmpresas(db)
	// mapear os produtos do banco de dados por CNPJ
	empMap := make(map[string]bool)
	// rodar nas empresas que estao no banco e adicionar true ao mapa pois estao no tabela
	for _, prod := range empDb {
		empMap[prod.CNPJ] = true
	}
	// rodar pelas empresas que retornaram do SOC
	for _, empApi := range empSoc {
		// formatando cnpj igual ao banco
		empApi.CNPJ = func(cnpj string) string {
			re := regexp.MustCompile(`[^\d]`)
			return re.ReplaceAllString(cnpj, "")
		}(empApi.CNPJ)
		// Se o CNPJ estiver vazio, continuar para a próxima iteração
		if strings.TrimSpace(empApi.CNPJ) == "" {
			// log.Println("CNPJ vazio, ignorando a empresa:", empApi.RazaoSocial)
			continue
		}
		// Verificar se o CNPJ já existe no banco de dados
		if !empMap[empApi.CNPJ] { // cnpj nao esta na tabela
			log.Printf("Inserindo nova empresa: Razao: %s, Codigo: %s, Cnpj: %s\n", empApi.RazaoSocial, empApi.CodEmpresa, empApi.CNPJ)
			// inserir o produto na tabela
			_, err := insertProduct(db, empApi)
			if err != nil {
				log.Printf("Erro ao inserir produto: %v\n", err)
			}
			// log.Printf("Id inserido na tabela %d\n", id)
			// Pausa por 5 segundo entre as inserções
			time.Sleep(2 * time.Second)
		} else {
			// log.Printf("Empresa com CNPJ %s já existe, ignorando.\n", empApi.CNPJ)
		}
	}
}

// funcao de pegar as empresas do SOC
func getEmpresas() []*Empresa {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={%27empresa%27:%27ID_EMPRESA%27,%27codigo%27:%27199197%27,%27chave%27:%2794666c79192a19a32dd5%27,%27tipoSaida%27:%27json%27,%27empresafiltro%27:%27%27,%27subgrupo%27:%27%27,%27socnet%27:%27%27,%27mostrarinativas%27:%27%27}"
	method := "POST"
	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Println("Erro ao criar requisição")
		log.Println(err)
	}
	res, err := client.Do(req)
	if err != nil {
		log.Println("Erro ao realizar requisição")
		log.Println(err)
	}
	defer res.Body.Close()
	// Lê o corpo da resposta como um stream de transformação, convertendo de ISO-8859-1 para UTF-8
	reader := transform.NewReader(res.Body, charmap.ISO8859_1.NewDecoder())
	// Lê o conteúdo transformado
	body, err := io.ReadAll(reader)
	if err != nil {
		log.Println("Erro ao ler corpo da resposta")
		log.Println(err)
	}
	var empresas []*Empresa
	err = json.Unmarshal(body, &empresas)
	if err != nil {
		log.Println("Erro ao unmarshal body")
		log.Println(err)
	}
	return empresas
}

// cria a tabela de empresas
func createProductTable(db *sql.DB) {
	query := `CREATE TABLE IF NOT EXISTS empresas (
    id SERIAL PRIMARY KEY,
    codigo BIGINT NOT NULL CHECK (codigo > 0),
    razao_social VARCHAR(150) NOT NULL,
    cnpj VARCHAR(18) NOT NULL
);`
	_, err := db.Exec(query)
	if err != nil {
		log.Println(err)
	}
}

// funcao que busca todos os produtos
func fetchEmpresas(db *sql.DB) []*Empresa {
	query := "SELECT codigo, razao_social, cnpj FROM empresas"
	rows, err := db.Query(query)
	if err != nil {
		log.Println(err)
	}
	defer rows.Close()
	var empresas []*Empresa
	for rows.Next() {
		var empresa Empresa
		err := rows.Scan(&empresa.CodEmpresa, &empresa.RazaoSocial, &empresa.CNPJ)
		if err != nil {
			log.Println(err)
		}
		empresas = append(empresas, &empresa)
	}
	if err = rows.Err(); err != nil {
		log.Println(err)
	}
	return empresas
}

// funcao para inserir os dados na tabela
func insertProduct(db *sql.DB, empresa *Empresa) (int, error) {
	query := `INSERT INTO empresas (codigo, razao_social, cnpj) 
	VALUES ($1, $2, $3) RETURNING id`
	// Função anônima para limpar o CNPJ
	empresa.CNPJ = func(cnpj string) string {
		re := regexp.MustCompile(`[^\d]`)
		return re.ReplaceAllString(cnpj, "")
	}(empresa.CNPJ)
	var id int
	err := db.QueryRow(query, empresa.CodEmpresa, strings.TrimSpace(empresa.RazaoSocial), empresa.CNPJ).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// função para verificar se a data é um feriado
func isHoliday(date time.Time) bool {
	const token string = "Key cm90ZWFkb3Jwcm90ZWdlcjpFTU9WR3JlcmRDRDdDcWtLRmcyNA=="

	feriados, err := func() ([]byte, error) {
		url := "https://clinicaproteger.http.msging.net/commands"
		method := "POST"
		payload := strings.NewReader(`
			{
    			"id": "120837as-0g89a=-sdgf8as-d9f8",
    			"method": "get",
    			"uri": "/resources/feriados"
			}
		`)
		// cria o cliente e a requisição
		client := &http.Client{}
		req, err := http.NewRequest(method, url, payload)
		if err != nil {
			log.Println("Erro ao montar a requisição - ERRO:", err)
			return nil, err
		}
		// adiciona os header da requisição
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Authorization", token)
		// realiza a requisição
		res, err := client.Do(req)
		if err != nil {
			log.Println("Erro ao realizar a requisição - ERRO:", err)
			return nil, err
		}
		defer res.Body.Close()
		// le o corpo da resposta da requisição feita
		body, err := io.ReadAll(res.Body)
		if err != nil {
			log.Println("Erro ao ler o corpo da requisição - ERRO:", err)
			return nil, err
		}
		return body, nil
	}()
	// verifica se a funcao anonima retornou algum erro
	if err != nil {
		log.Printf("Erro ao trazer resources do Blip, Error: %v", err)
		return false
	}
	// struct para lidar com os feriados resource
	type responseBlip struct {
		Resource string `json:"resource"`
	}
	var resource responseBlip
	err = json.Unmarshal(feriados, &resource)
	if err != nil {
		log.Println("Erro ao transformar a resposta - ERRO:", err)
		return false
	}
	log.Println("Resource:", resource.Resource)
	// Parse do retorno para intervalos de datas
	diasFeriado, err := parseDateIntervals(resource.Resource)
	if err != nil {
		log.Println("Erro ao processar intervalos de feriados - ERRO:", err)
		return false
	}
	// Verifica se a data está em um dos intervalos de feriados
	return isDateInIntervals(date, diasFeriado)
}

// funcao para processar a string e extrar os intervalos de datas
func parseDateIntervals(input string) ([][2]time.Time, error) {
	diasFeriadoTime := []([2]time.Time){}
	// divide a string por -
	arrayDiasFeriado := strings.Split(input, "-")
	for _, diaFeriado := range arrayDiasFeriado {
		// log.Println("Dia feriado:", diaFeriado)
		// converte cada segmento para uma data
		diaParseado, err := time.Parse("02/01", diaFeriado)
		if err != nil {
			log.Println("Erro ao analisar a data:", diaFeriado, "- ERRO:", err)
			return nil, err
		}
		// log.Println("Dia feriado parseado:", diaParseado)
		// adiciona a data ao array de intervalos
		diasFeriadoTime = append(diasFeriadoTime, [2]time.Time{diaParseado, diaParseado})
	}
	// log.Println("Dias de feriado Parseado:", diasFeriadoTime)
	// retorna os dias de feriado
	return diasFeriadoTime, nil
}

// funcao para verificar se uma data esta dentro de um dos intervalos
func isDateInIntervals(date time.Time, intervals [][2]time.Time) bool {
	log.Println("Dia:", date)
	// log.Println("Intervalos de feriados", intervals)
	for _, interval := range intervals {
		log.Println("Dia Feriado:", interval[0])
		if date.Month() == interval[0].Month() && date.Day() == interval[0].Day() {
			log.Printf("Dia %v é um feriado (igual a %v)", date, interval[0])
			return true
		}
	}
	return false
}

// funcao que busca somente um produto
func fetchProductByCnpj(cnpj string) (*Empresa, error) {
	// conexao com banco
	connStr := "postgresstringconnection"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	// fechar conexao apos execução
	defer db.Close()
	// funcao de testar conexao
	if err = db.Ping(); err != nil {
		log.Println(err)
		return nil, err
	}
	query := "SELECT codigo, razao_social, cnpj FROM empresas WHERE cnpj = $1"
	var empresa Empresa
	err = db.QueryRow(query, cnpj).Scan(&empresa.CodEmpresa, &empresa.RazaoSocial, &empresa.CNPJ)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No rows found with this cnpj: %s\n", cnpj)
			return nil, err
		}
		log.Println(err)
		return nil, err
	}
	return &empresa, nil
}

// funcao para trazer os setores do SOC
func fetchSetorSOC() ([]Setor, error) {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={'empresa':'ID_EMPRESA','codigo':'CODIGO_FUNCAO','chave':'CHAVE_FUNCAO','tipoSaida':'json'}"
	method := "POST"
	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	// Convertendo o body para UTF-8
	utf8Body, err := decodeToUTF8(body)
	if err != nil {
		return nil, err
	}
	var setores []Setor
	err = json.Unmarshal([]byte(utf8Body), &setores)
	if err != nil {
		return nil, err
	}
	return setores, nil
}

// funcao para pegar os cpfs
func getCpfSoc(empresa, cpf string) ([]byte, error) {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={'empresa':'ID_EMPRESA','codigo':'CODIGO_FUNCAO','chave':'CHAVE_FUNCAO','tipoSaida':'json','empresaTrabalho':'" + empresa + "','cpf':'" + cpf + "','parametroData':'0','dataInicio':'','dataFim':''}"
	method := "POST"
	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Println("problema em criar a requisição")
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		log.Println("erro ao realizar a requisição")
		return nil, err
	}
	defer res.Body.Close()
	// ler o corpo da resposta
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println("erro ao ler a resposta")
		return nil, err
	}
	// decodificando o corpo da resposta para utf8
	utf8Body, err := decodeToUTF8(body)
	if err != nil {
		return nil, err
	}
	// retornando o copor decodificado em utf8
	return []byte(utf8Body), nil
}

// funcao para trazer a hierarquia dos setores-cargos
func fetchHierarquia(empresa string) ([]Cargos_Setores, error) {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={'empresa':'" + empresa + "','codigo':'CODIGO_FUNCAO','chave':'CHAVE_FUNCAO','tipoSaida':'json'}"
	method := "POST"
	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	// Lê o corpo da resposta como um stream de transformação, convertendo de ISO-8859-1 para UTF-8
	reader := transform.NewReader(res.Body, charmap.ISO8859_1.NewDecoder())
	body, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("erro ao ler o corpo da resposta: %v", err)
		return nil, err
	}
	// Verificando se o body contém a mensagem de erro
	if strings.Contains(string(body), "Não encontrado exporta dados codigo/Empresa") {
		return nil, fmt.Errorf("erro: Não encontrado exporta dados codigo/Empresa")
	}
	// adicionando o retorno da requisição a uma variavel para lidarmos no codigo
	var hierarquia []Cargos_Setores
	err = json.Unmarshal(body, &hierarquia)
	if err != nil {
		log.Printf("erro ao transformar cargos e setores em objeto Cargo_Setores: %v", err)
		return nil, err
	}
	return hierarquia, nil
}

// funcao para codificar tudo em utf8
func decodeToUTF8(data []byte) (string, error) {
	if utf8.Valid(data) {
		// Se os dados já estiverem em UTF-8, retornar diretamente
		return string(data), nil
	}
	reader := bytes.NewReader(data)
	// Detecta e converte o charset para UTF-8
	utf8Reader, err := charset.NewReader(reader, "")
	if err != nil {
		return "", err
	}
	result, err := io.ReadAll(utf8Reader)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// funcao para gerar o nonce
func generateNonce() string {
	nonce := make([]byte, 16)
	_, err := rand.Read(nonce)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(nonce)
}

// funcao para gerar o passwordigest
func createPasswordDigest(nonce, created, password string) string {
	nonceBytes, _ := base64.StdEncoding.DecodeString(nonce)
	combined := append(nonceBytes, []byte(created+password)...)
	hash := sha1.Sum(combined)
	return base64.StdEncoding.EncodeToString(hash[:])
}

// Função para gerar o cabeçalho de autenticação WS-Security
func createWSSecurityHeader(username, password string) *etree.Element {
	nonce := generateNonce()
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	passwordDigest := createPasswordDigest(nonce, created, password)
	// Security
	security := etree.NewElement("wsse:Security")
	security.CreateAttr("xmlns:wsse", "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd")
	security.CreateAttr("xmlns:wsu", "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd")
	// Timestamp
	timestamp := security.CreateElement("wsu:Timestamp")
	timestamp.CreateAttr("wsu:Id", "Timestamp-"+created)
	createdTime := timestamp.CreateElement("wsu:Created")
	createdTime.SetText(created)
	expiresTime := timestamp.CreateElement("wsu:Expires")
	expiresTime.SetText(time.Now().UTC().Add(10 * time.Minute).Format("2006-01-02T15:04:05Z"))
	// UsernameToken
	token := security.CreateElement("wsse:UsernameToken")
	token.CreateAttr("xmlns:wsu", "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd")
	token.CreateAttr("wsu:Id", "SecurityToken-"+created)
	// Username
	user := token.CreateElement("wsse:Username")
	user.SetText(username)
	// Password (PasswordDigest)
	pass := token.CreateElement("wsse:Password")
	pass.CreateAttr("Type", "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest")
	pass.SetText(passwordDigest)
	// Nonce
	nonceElem := token.CreateElement("wsse:Nonce")
	nonceElem.CreateAttr("EncodingType", "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary")
	nonceElem.SetText(nonce)
	// Created
	createdElem := token.CreateElement("wsu:Created")
	createdElem.SetText(created)

	return security
}

// Função para criar o corpo da requisição SOAP
func createSOAPBody(date, hour, tipoCompromisso, empresa, codigoFuncionario, codigoAgenda string) (*etree.Element, error) {
	if codigoFuncionario == "" {
		// Cria o elemento para o corpo da requisição
		bodyElem := etree.NewElement("tns:incluirAgendamento")
		agendamento := etree.NewElement("IncluirAgendamentoWsVo")
		// Cria os elementos de identificação
		identificacaoElem := etree.NewElement("identificacaoWsVo")
		identificacaoElem.CreateElement("codigoEmpresaPrincipal").SetText("ID_EMPRESA")
		identificacaoElem.CreateElement("codigoResponsavel").SetText("CODIGO_RESPONSAVEL")
		identificacaoElem.CreateElement("codigoUsuario").SetText("COD_USUARIO")
		agendamento.AddChild(identificacaoElem)
		// Cria os elementos de agendamento
		dadosElem := etree.NewElement("dadosAgendamentoWsVo")
		dadosElem.CreateElement("tipoBuscaEmpresa").SetText("CODIGO_SOC")
		dadosElem.CreateElement("codigoEmpresa").SetText(empresa)
		dadosElem.CreateElement("reservarCompromissoParaEmpresa").SetText("true")
		dadosElem.CreateElement("codigoUsuarioAgenda").SetText(codigoAgenda)
		dadosElem.CreateElement("data").SetText(date)
		dadosElem.CreateElement("horaInicial").SetText(hour)
		// nao obrigatorio
		dadosElem.CreateElement("codigoCompromisso").SetText("2")
		dadosElem.CreateElement("tipoCompromisso").SetText(tipoCompromisso)
		dadosElem.CreateElement("atendido").SetText("AGUARDANDO")
		agendamento.AddChild(dadosElem)
		bodyElem.AddChild(agendamento)
		return bodyElem, nil
	} else {
		// Cria o elemento para o corpo da requisição
		bodyElem := etree.NewElement("tns:incluirAgendamento")
		agendamento := etree.NewElement("IncluirAgendamentoWsVo")
		// Cria os elementos de identificação
		identificacaoElem := etree.NewElement("identificacaoWsVo")
		identificacaoElem.CreateElement("codigoEmpresaPrincipal").SetText("ID_EMPRESA")
		identificacaoElem.CreateElement("codigoResponsavel").SetText("CODIGO_RESPONSAVEL")
		identificacaoElem.CreateElement("codigoUsuario").SetText("COD_USUARIO")
		agendamento.AddChild(identificacaoElem)
		// Cria os elementos de agendamento
		dadosElem := etree.NewElement("dadosAgendamentoWsVo")
		dadosElem.CreateElement("tipoBuscaEmpresa").SetText("CODIGO_SOC")
		dadosElem.CreateElement("codigoEmpresa").SetText(empresa)
		dadosElem.CreateElement("reservarCompromissoParaEmpresa").SetText("false")
		dadosElem.CreateElement("tipoBuscaFuncionario").SetText("CODIGO_SOC")
		dadosElem.CreateElement("codigoFuncionario").SetText(codigoFuncionario)
		dadosElem.CreateElement("codigoUsuarioAgenda").SetText(codigoAgenda)
		dadosElem.CreateElement("data").SetText(date)
		dadosElem.CreateElement("horaInicial").SetText(hour)
		// nao obrigatorio
		dadosElem.CreateElement("codigoCompromisso").SetText("2")
		dadosElem.CreateElement("tipoCompromisso").SetText(tipoCompromisso)
		dadosElem.CreateElement("atendido").SetText("AGUARDANDO")
		agendamento.AddChild(dadosElem)
		bodyElem.AddChild(agendamento)

		return bodyElem, nil
	}
}

// Função para enviar a requisição SOAP
func sendSOAPRequest(soapBody, securityHeader *etree.Element) error {
	doc := etree.NewDocument()
	envelope := doc.CreateElement("soap:Envelope")
	envelope.CreateAttr("xmlns:soap", "http://schemas.xmlsoap.org/soap/envelope/")
	envelope.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	envelope.CreateAttr("xmlns:tns", "http://services.soc.age.com/") // Adiciona o espaço de nomes
	// header
	header := envelope.CreateElement("soap:Header")
	header.AddChild(securityHeader)
	// Body
	body := envelope.CreateElement("soap:Body")
	body.AddChild(soapBody) // Adiciona o corpo diretamente
	// Converta o XML para string
	doc.Indent(2)
	xmlString, err := doc.WriteToString()
	if err != nil {
		log.Println("Error converting XML to string:", err)
		return err
	}
	// Adiciona a declaração XML
	xmlHeader := `<?xml version="1.0" encoding="utf-8"?>`
	xmlString = xmlHeader + "\n" + xmlString
	// Imprimir a string XML para depuração
	log.Println(xmlString)
	// Enviar a requisição SOAP
	url := "https://ws1.soc.com.br/WSoc/AgendamentoWs?wsdl"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte(xmlString)))
	if err != nil {
		log.Println("Error creating request:", err)
		return err
	}
	// seta o header, o cliente e executa e a requisição
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error sending request:", err)
		return err
	}
	defer resp.Body.Close()
	// abaixo le a resposta da requisição e printa no log
	// log.Println(resp)
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error reading response:", err)
		return err
	}
	log.Println(string(bodyBytes))
	return nil
}

// funcao de criar agendamento
func createAgendamento(date, hour, compromisso, empresa, codigoFuncionario, codigoAgenda string) error {
	// Cabeçalho de segurança
	securityHeader := createWSSecurityHeader("USER", "PASSWORD")
	// Corpo da requisição SOAP
	log.Println(date, hour, compromisso, empresa)
	soapBody, err := createSOAPBody(date, hour, compromisso, empresa, codigoFuncionario, codigoAgenda)
	if err != nil {
		log.Printf("Erro ao criar corpo: %v", err)
		return err
	}
	// Enviar a requisição
	err = sendSOAPRequest(soapBody, securityHeader)
	if err != nil {
		log.Printf("Erro ao realizar requisição: %v", err)
		return err
	}
	// foi
	return nil
}

// criação de funcionario
func createFuncionario(codigoCargo, nomeCargo, codigoEmpresa, cpf, dataNascimento, nomeFuncionario, codigoSetor, nomeSetor, rg, telefone, nomeEmpresa, cnpjEmpresa, pis string) (string, error) {
	// Cabeçalho de segurança
	securityHeader := createWSSecurityHeader("USER", "PASSWORD")
	// Corpo da requisição SOAP
	log.Println()
	soapBody, err := createSOAPBodyFuncionario(codigoCargo, nomeCargo, codigoEmpresa, cpf, dataNascimento, nomeFuncionario, codigoSetor, nomeSetor, rg, telefone, nomeEmpresa, cnpjEmpresa, pis)
	if err != nil {
		log.Printf("Erro ao criar corpo: %v", err)
		return "", err
	}
	// Enviar a requisição
	createResponse, err := sendSOAPRequestFuncionario(soapBody, securityHeader)
	if err != nil {
		log.Printf("Erro ao realizar requisição: %v", err)
		return "", err
	}
	var resp xmlResponse
	// pegar o codigoFuncionario da resposta
	err = xml.Unmarshal(createResponse, &resp)
	if err != nil {
		log.Printf("Erro ao realizar trasnformação de xml para object: %v", err)
		return "", err
	}
	if resp.Body.Fault != nil {
		log.Println("ocorreu um erro ao criar funcionario:", resp.Body.Fault.Faultstring)
		return "", nil
	}
	codigoF := resp.Body.ImportacaoFuncionarioResponse.FuncionarioRetorno.CodigoFuncionario
	log.Println("codigoFuncionario:", codigoF)
	return codigoF, nil
}

func createSOAPBodyFuncionario(codigoCargo, nomeCargo, codigoEmpresa, cpf, dataNascimento, nomeFuncionario, codigoSetor, nomeSetor, rg, tel, nomeEmpresa, cnpjEmpresa, pis string) (*etree.Element, error) {
	matricula := strconv.FormatInt(time.Now().Unix(), 10)
	// Obtém a data atual
	dataAtual := time.Now()
	dataAdmissao := dataAtual.Format("02/01/2006")
	// Cria o elemento para o corpo da requisição
	bodyElem := etree.NewElement("ser:importacaoFuncionario")
	// criar o elementro que mostra q estamos criando o funcionario
	funcionario := etree.NewElement("Funcionario")
	funcionario.CreateElement("criarFuncionario").SetText("true")
	funcionario.CreateElement("criarUnidade").SetText("true")
	// Cria os elementos de identificação
	identificacaoElem := etree.NewElement("identificacaoWsVo")
	identificacaoElem.CreateElement("chaveAcesso").SetText(" ") // 	A informação pode ser consultada nas configurações de integração no cadastro de Empresa
	identificacaoElem.CreateElement("codigoEmpresaPrincipal").SetText("ID_EMPRESA")
	identificacaoElem.CreateElement("codigoResponsavel").SetText("CODIGO_REPONSAVEL")
	identificacaoElem.CreateElement("codigoUsuario").SetText("COD_USUARIO")
	funcionario.AddChild(identificacaoElem)
	// Cria os elementos do cargo
	dadosCargo := etree.NewElement("cargoWsVo")
	dadosCargo.CreateElement("codigo").SetText(strings.ToUpper(codigoCargo))
	dadosCargo.CreateElement("codigoRh").SetText(" ")
	dadosCargo.CreateElement("nome").SetText(strings.ToUpper(nomeCargo))
	dadosCargo.CreateElement("tipoBusca").SetText("CODIGO")
	funcionario.AddChild(dadosCargo)
	// Cria os elementos do setor
	dadosSetor := etree.NewElement("setorWsVo")
	dadosSetor.CreateElement("codigo").SetText(strings.ToUpper(codigoSetor))
	dadosSetor.CreateElement("codigoRh").SetText(" ")
	dadosSetor.CreateElement("nome").SetText(strings.ToUpper(nomeSetor))
	dadosSetor.CreateElement("tipoBusca").SetText("CODIGO")
	funcionario.AddChild(dadosSetor)
	// Cria os elementos do funcionario
	dadosFuncionario := etree.NewElement("funcionarioWsVo")
	dadosFuncionario.CreateElement("naoPossuiPis").SetText("true")
	dadosFuncionario.CreateElement("pis").SetText(strings.ToUpper(pis))
	dadosFuncionario.CreateElement("chaveProcuraFuncionario").SetText("CPF")
	dadosFuncionario.CreateElement("codigo").SetText("")
	dadosFuncionario.CreateElement("codigoEmpresa").SetText(strings.ToUpper(codigoEmpresa))
	dadosFuncionario.CreateElement("cpf").SetText(strings.ToUpper(cpf))
	dadosFuncionario.CreateElement("dataAdmissao").SetText(strings.ToUpper(dataAdmissao))
	dadosFuncionario.CreateElement("dataNascimento").SetText(strings.ToUpper(dataNascimento))
	dadosFuncionario.CreateElement("estadoCivil").SetText("SOLTEIRO")
	dadosFuncionario.CreateElement("naoPossuiMatriculaRh").SetText("true")
	dadosFuncionario.CreateElement("matricula").SetText(strings.ToUpper(matricula))
	dadosFuncionario.CreateElement("telefoneCelular").SetText(strings.ToUpper(tel))
	dadosFuncionario.CreateElement("nomeFuncionario").SetText(strings.ToUpper(nomeFuncionario))
	dadosFuncionario.CreateElement("regimeTrabalho").SetText("NORMAL")
	dadosFuncionario.CreateElement("sexo").SetText("MASCULINO")
	dadosFuncionario.CreateElement("situacao").SetText("ATIVO")
	dadosFuncionario.CreateElement("tipoBuscaEmpresa").SetText("CODIGO_SOC")
	dadosFuncionario.CreateElement("tipoContratacao").SetText("CLT")
	dadosFuncionario.CreateElement("nomeSocial").SetText(strings.ToUpper(nomeFuncionario))
	dadosFuncionario.CreateElement("rg").SetText(rg)
	dadosFuncionario.CreateElement("tipoAdmissao").SetText("ADMISSAO")
	dadosFuncionario.CreateElement("codigoCategoriaESocial").SetText("101")
	funcionario.AddChild(dadosFuncionario)
	// Cria os elementos de unidadeWsVo
	unidade := etree.NewElement("unidadeWsVo")
	unidade.CreateElement("codigo").SetText("038")
	unidade.CreateElement("codigoRh").SetText("")
	unidade.CreateElement("nome").SetText(strings.ToUpper(nomeEmpresa))
	unidade.CreateElement("razaoSocial").SetText(strings.ToUpper(nomeEmpresa))
	unidade.CreateElement("cnpj_cei").SetText("CNPJ")
	unidade.CreateElement("codigoCnpjCei").SetText(strings.ToUpper(formataCNPJ(cnpjEmpresa)))
	unidade.CreateElement("tipoBusca").SetText("CODIGO")
	funcionario.AddChild(unidade)
	// adiciona todos os elementos de funcionario para o principal
	bodyElem.AddChild(funcionario)
	// retorna o elemento principal
	return bodyElem, nil
}

// formataCNPJ formata um CNPJ para o padrão XX.XXX.XXX/XXXX-XX
func formataCNPJ(cnpj string) string {
	// Remove todos os caracteres não numéricos
	cnpj = strings.ReplaceAll(cnpj, ".", "")
	cnpj = strings.ReplaceAll(cnpj, "/", "")
	cnpj = strings.ReplaceAll(cnpj, "-", "")

	// Verifica se o CNPJ tem exatamente 14 dígitos
	if len(cnpj) != 14 {
		return cnpj // Retorna o valor original caso esteja inválido
	}

	// Formata o CNPJ no padrão desejado
	return fmt.Sprintf("%s.%s.%s/%s-%s",
		cnpj[:2],   // XX
		cnpj[2:5],  // XXX
		cnpj[5:8],  // XXX
		cnpj[8:12], // XXXX
		cnpj[12:],  // XX
	)
}

// Função para enviar a requisição SOAP
func sendSOAPRequestFuncionario(soapBody, securityHeader *etree.Element) ([]byte, error) {
	doc := etree.NewDocument()
	envelope := doc.CreateElement("soapenv:Envelope")
	envelope.CreateAttr("xmlns:soapenv", "http://schemas.xmlsoap.org/soap/envelope/")
	//envelope.CreateAttr("xmlns:xsi", "http://www.w3.org/2001/XMLSchema-instance")
	envelope.CreateAttr("xmlns:ser", "http://services.soc.age.com/")
	// header
	header := envelope.CreateElement("soapenv:Header")
	header.AddChild(securityHeader)
	// Body
	body := envelope.CreateElement("soapenv:Body")
	body.AddChild(soapBody) // Adiciona o corpo diretamente
	// Converta o XML para string
	doc.Indent(2)
	xmlString, err := doc.WriteToString()
	if err != nil {
		log.Println("Error converting XML to string:", err)
		return nil, err
	}
	// Adiciona a declaração XML
	xmlHeader := `<?xml version="1.0" encoding="utf-8"?>`
	xmlString = xmlHeader + "\n" + xmlString
	// Imprimir a string XML para depuração
	log.Println("Request:", xmlString)
	// Enviar a requisição SOAP
	url := "https://ws1.soc.com.br/WSoc/FuncionarioModelo2Ws?wsdl"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer([]byte(xmlString)))
	if err != nil {
		log.Println("Error creating request:", err)
		return nil, err
	}
	// seta o header, o cliente e executa e a requisição
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error sending request:", err)
		return nil, err
	}
	defer resp.Body.Close()
	// abaixo le a resposta da requisição e printa no log
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println("Error reading response:", err)
		return nil, err
	}
	log.Println("Response:", string(bodyBytes))
	return bodyBytes, nil
}

// pegar agendamentos requisição

// funcao para pesquisar os agendamentos dentro do SOC
func getAgendamento(diaInicio, mesInicio, anoInicio, diaFim, mesFim, anoFim string) ([]byte, error) {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={'empresa':'ID_EMPRESA','codigo':'CODIGO_FUNCAO','chave':'CHAVE_FUNCAO','tipoSaida':'json','empresaTrabalho':'ID_EMPRESA','dataInicio':'" + diaInicio + "%2F" + mesInicio + "%2F" + anoInicio + "','dataFim':'" + diaFim + "%2F" + mesFim + "%2F" + anoFim + "','codigoAgenda':'3015983','statusAgendaFiltro':''}"
	method := "POST"
	payload := strings.NewReader(``)
	// criar o client que executara a requisição e a requisição
	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		log.Printf("Erro ao criar requisição: %v", err)
		return nil, err
	}
	// executar a requisição
	res, err := client.Do(req)
	if err != nil {
		log.Printf("Erro ao realizar requisição: %v", err)
		return nil, err
	}
	defer res.Body.Close()
	// ler o corpo da requisição
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("Erro ao ler corpo da requisição SOC: %v", err)
		return nil, err
	}
	return body, nil
}
func getAgendaProteger(diaInicio, mesInicio, anoInicio, diaFim, mesFim, anoFim string) ([]byte, error) {
	url := "https://ws1.soc.com.br/WebSoc/exportadados?parametro={'empresa':'ID_EMPRESA','codigo':'CODIGO_FUNCAO','chave':'CHAVE_FUNCAO','tipoSaida':'json','empresaTrabalho':'ID_EMPRESA','dataInicio':'" + diaInicio + "%2F" + mesInicio + "%2F" + anoInicio + "','dataFim':'" + diaFim + "%2F" + mesFim + "%2F" + anoFim + "','codigoAgenda':'COD_AGENDA','statusAgendaFiltro':''}"
	method := "POST"
	payload := strings.NewReader(``)
	// criar o client que executara a requisição e a requisição
	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		log.Printf("Erro ao criar requisição: %v", err)
		return nil, err
	}
	// executar a requisição
	res, err := client.Do(req)
	if err != nil {
		log.Printf("Erro ao realizar requisição: %v", err)
		return nil, err
	}
	defer res.Body.Close()
	// ler o corpo da requisição
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("Erro ao ler corpo da requisição SOC: %v", err)
		return nil, err
	}
	return body, nil
}

// horarios agendamento
type Horario struct {
	Data    string `json:"data"`
	Horario string `json:"horario"`
}

// funcionario strutura
type Funcionario struct {
	Nome              string `json:"NOME"`
	Cpf               string `json:"CPFFUNCIONARIO"`
	Cargo             string `json:"NOMECARGO"`
	CodigoFuncionario string `json:"CODIGO"`
}

// estrutura da empresa
type Empresa struct {
	CodEmpresa  string `json:"codigo"`
	RazaoSocial string `json:"razaoSocial"`
	CNPJ        string `json:"cnpj"`
}

// estrutura de setores
type Setor struct {
	CodigoEmpresa string `json:"CODIGOEMPRESA"`
	NomeSetor     string `json:"NOMESETOR"`
	SetorAtivo    string `json:"SETORATIVO"`
	CodigoSetor   string `json:"CODIGOSETOR"`
}

// setor response
type SetorResponse struct {
	ID    string `json:"id"`
	Setor string `json:"setor"`
}

// estrutura para cargos e setores
type Cargos_Setores struct {
	CodigoSetor string `json:"CODIGO_SETOR"`
	NomeSetor   string `json:"NOME_SETOR"`
	AtivoSetor  string `json:"ATIVO_SETOR"`
	CodigoCargo string `json:"CODIGO_CARGO"`
	NomeCargo   string `json:"NOME_CARGO"`
	AtivoCargo  string `json:"ATIVO_CARGO"`
}

// estrutura de cargos response
type CargoResponse struct {
	ID    string `json:"id"`
	Cargo string `json:"cargo"`
}

// struct para transformar o body em objGo
type FuncionarioReq struct {
	CodigoEmpresa   string `json:"codigoEmpresa"`
	NomeEmpresa     string `json:"nomeEmpresa"`
	CNPJEmpresa     string `json:"cnpjEmpresa"`
	CodigoCargo     string `json:"codigoCargo"`
	NomeCargo       string `json:"nomeCargo"`
	CodigoSetor     string `json:"codigoSetor"`
	NomeSetor       string `json:"nomeSetor"`
	CPF             string `json:"cpf"`
	DataNascimento  string `json:"dataNascimento"`
	NomeFuncionario string `json:"nomeFuncionario"`
	RG              string `json:"rg"`
	Telefone        string `json:"telefone"`
	Pis             string `json:"pis"`
}

// struct para pegar o codigoFuncionario
type xmlResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Text    string   `xml:",chardata"`
	Soap    string   `xml:"soap,attr"`
	Body    struct {
		Text                          string `xml:",chardata"`
		ImportacaoFuncionarioResponse struct {
			Text               string `xml:",chardata"`
			Ns2                string `xml:"ns2,attr"`
			FuncionarioRetorno struct {
				Text              string `xml:",chardata"`
				CodigoFuncionario string `xml:"codigoFuncionario"`
			} `xml:"FuncionarioRetorno"`
		} `xml:"importacaoFuncionarioResponse"`
		Fault *struct {
			Text        string `xml:",chardata"`
			Faultcode   string `xml:"faultcode"`
			Faultstring string `xml:"faultstring"`
			Detail      struct {
				Text        string `xml:",chardata"`
				WSException struct {
					Text string `xml:",chardata"`
					Ns1  string `xml:"ns1,attr"`
				} `xml:"WSException"`
			} `xml:"detail"`
		} `xml:"Fault"`
	} `xml:"Body"`
}
