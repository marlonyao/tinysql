package sql

import (
	"fmt"
	"strconv"
	"strings"
)

// === AST 节点 ===

type Statement interface {
	stmtNode()
}

type CreateTableStmt struct {
	TableName string
	Columns   []ColumnDef
}

func (s *CreateTableStmt) stmtNode() {}

type InsertStmt struct {
	TableName string
	Columns   []string
	Values    []Expr
}

func (s *InsertStmt) stmtNode() {}

type SelectStmt struct {
	Columns   []string // "*" 或具体列名
	TableName string
	Where     Expr // 可为 nil
}

func (s *SelectStmt) stmtNode() {}

type ColumnDef struct {
	Name     string
	Type     string // "INT", "VARCHAR", "BOOL"
	Nullable bool
}

// Expr 表达式接口
type Expr interface {
	exprNode()
	String() string
}

type BinaryExpr struct {
	Op    string // =, <>, <, >, <=, >=, AND, OR
	Left  Expr
	Right Expr
}

func (e *BinaryExpr) exprNode() {}
func (e *BinaryExpr) String() string { return fmt.Sprintf("(%s %s %s)", e.Left.String(), e.Op, e.Right.String()) }

type Identifier struct {
	Name string
}

func (e *Identifier) exprNode() {}
func (e *Identifier) String() string { return e.Name }

type Literal struct {
	Value interface{} // int, string, bool, nil
}

func (e *Literal) exprNode() {}
func (e *Literal) String() string {
	if e.Value == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", e.Value)
}

// === 分词器 (Tokenizer) ===

type Token struct {
	Type  TokenType
	Value string
}

type TokenType int

const (
	TokenEOF TokenType = iota
	TokenKeyword // CREATE, TABLE, INSERT, INTO, SELECT, FROM, WHERE, AND, OR, NOT, NULL, INT, VARCHAR, BOOL, VALUES
	TokenIdentifier
	TokenNumber
	TokenString
	TokenSymbol // (, ), ,, ;, =, <, >, <=, >=, <>
)

var keywords = map[string]struct{}{
	"CREATE": {}, "TABLE": {}, "INSERT": {}, "INTO": {},
	"SELECT": {}, "FROM": {}, "WHERE": {}, "AND": {},
	"OR": {}, "NOT": {}, "NULL": {}, "INT": {},
	"VARCHAR": {}, "TEXT": {}, "BOOL": {}, "VALUES": {}, "TRUE": {},
	"FALSE": {}, "PRIMARY": {}, "KEY": {},
}

func tokenize(input string) ([]Token, error) {
	var tokens []Token
	i := 0
	for i < len(input) {
		c := input[i]

		// 跳过空白
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}

		// 字符串字面量 '...'
		if c == '\'' {
			j := i + 1
			for j < len(input) && input[j] != '\'' {
				j++
			}
			if j >= len(input) {
				return nil, fmt.Errorf("unterminated string at %d", i)
			}
			tokens = append(tokens, Token{Type: TokenString, Value: input[i+1 : j]})
			i = j + 1
			continue
		}

		// 数字
		if isDigit(c) {
			j := i
			for j < len(input) && isDigit(input[j]) {
				j++
			}
			tokens = append(tokens, Token{Type: TokenNumber, Value: input[i:j]})
			i = j
			continue
		}

		// 标识符/关键字
		if isLetter(c) || c == '_' {
			j := i
			for j < len(input) && (isLetter(input[j]) || isDigit(input[j]) || input[j] == '_') {
				j++
			}
			word := strings.ToUpper(input[i:j])
			if _, ok := keywords[word]; ok {
				tokens = append(tokens, Token{Type: TokenKeyword, Value: word})
			} else {
				tokens = append(tokens, Token{Type: TokenIdentifier, Value: input[i:j]})
			}
			i = j
			continue
		}

		// 符号
		if c == ',' || c == '(' || c == ')' || c == ';' || c == '*' {
			tokens = append(tokens, Token{Type: TokenSymbol, Value: string(c)})
			i++
			continue
		}

		// 比较运算符: <>, <=, >=, <, >, =
		if c == '<' {
			if i+1 < len(input) && input[i+1] == '>' {
				tokens = append(tokens, Token{Type: TokenSymbol, Value: "<>"})
				i += 2
			} else if i+1 < len(input) && input[i+1] == '=' {
				tokens = append(tokens, Token{Type: TokenSymbol, Value: "<="})
				i += 2
			} else {
				tokens = append(tokens, Token{Type: TokenSymbol, Value: "<"})
				i++
			}
			continue
		}
		if c == '>' {
			if i+1 < len(input) && input[i+1] == '=' {
				tokens = append(tokens, Token{Type: TokenSymbol, Value: ">="})
				i += 2
			} else {
				tokens = append(tokens, Token{Type: TokenSymbol, Value: ">"})
				i++
			}
			continue
		}
		if c == '=' {
			tokens = append(tokens, Token{Type: TokenSymbol, Value: "="})
			i++
			continue
		}

		return nil, fmt.Errorf("unexpected character '%c' at %d", c, i)
	}
	tokens = append(tokens, Token{Type: TokenEOF})
	return tokens, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// === 递归下降 Parser ===

type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(input string) (*Parser, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}
	return &Parser{tokens: tokens}, nil
}

func (p *Parser) peek() Token   { return p.tokens[p.pos] }
func (p *Parser) advance() Token { t := p.tokens[p.pos]; p.pos++; return t }
func (p *Parser) expect(t TokenType, values ...string) (Token, error) {
	cur := p.peek()
	if cur.Type != t {
		return Token{}, fmt.Errorf("expected %v, got %v (%s)", t, cur.Type, cur.Value)
	}
	if len(values) > 0 {
		found := false
		for _, v := range values {
			if strings.EqualFold(cur.Value, v) {
				found = true
				break
			}
		}
		if !found {
			return Token{}, fmt.Errorf("expected one of %v, got %s", values, cur.Value)
		}
	}
	return p.advance(), nil
}

// Parse 解析 SQL 语句
func (p *Parser) Parse() (Statement, error) {
	tok := p.peek()
	if tok.Type != TokenKeyword {
		return nil, fmt.Errorf("expected statement keyword, got %s", tok.Value)
	}

	switch strings.ToUpper(tok.Value) {
	case "CREATE":
		return p.parseCreateTable()
	case "INSERT":
		return p.parseInsert()
	case "SELECT":
		return p.parseSelect()
	default:
		return nil, fmt.Errorf("unsupported statement: %s", tok.Value)
	}
}

// parseCreateTable: CREATE TABLE name (col1 INT, col2 VARCHAR(255), ...)
func (p *Parser) parseCreateTable() (*CreateTableStmt, error) {
	p.advance() // CREATE
	p.expect(TokenKeyword, "TABLE")
	tok, _ := p.expect(TokenIdentifier)
	stmt := &CreateTableStmt{TableName: tok.Value}

	p.expect(TokenSymbol, "(")
	for {
		colTok, _ := p.expect(TokenIdentifier)
		col := ColumnDef{Name: colTok.Value}

		// 类型
		typeTok, _ := p.expect(TokenKeyword)
		col.Type = strings.ToUpper(typeTok.Value)
		if col.Type == "VARCHAR" {
			// 可选的 (size)
			if p.peek().Value == "(" {
				p.advance()
				p.expect(TokenNumber)
				p.expect(TokenSymbol, ")")
			}
		}

		// 可选的 NULL/NOT NULL
		if strings.EqualFold(p.peek().Value, "NOT") {
			p.advance()
			p.expect(TokenKeyword, "NULL")
			col.Nullable = false
		} else if strings.EqualFold(p.peek().Value, "NULL") {
			p.advance()
			col.Nullable = true
		} else {
			col.Nullable = true // 默认 nullable
		}

		stmt.Columns = append(stmt.Columns, col)

		if p.peek().Value == ")" {
			break
		}
		p.expect(TokenSymbol, ",")
	}
	p.expect(TokenSymbol, ")")
	p.expect(TokenSymbol, ";")

	return stmt, nil
}

// parseInsert: INSERT INTO name (col1, col2) VALUES (val1, val2);
func (p *Parser) parseInsert() (*InsertStmt, error) {
	p.advance() // INSERT
	p.expect(TokenKeyword, "INTO")
	tok, _ := p.expect(TokenIdentifier)
	stmt := &InsertStmt{TableName: tok.Value}

	p.expect(TokenSymbol, "(")
	for {
		colTok, _ := p.expect(TokenIdentifier)
		stmt.Columns = append(stmt.Columns, colTok.Value)
		if p.peek().Value == ")" {
			break
		}
		p.expect(TokenSymbol, ",")
	}
	p.expect(TokenSymbol, ")")

	p.expect(TokenKeyword, "VALUES")
	p.expect(TokenSymbol, "(")
	for {
		expr, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		stmt.Values = append(stmt.Values, expr)
		if p.peek().Value == ")" {
			break
		}
		p.expect(TokenSymbol, ",")
	}
	p.expect(TokenSymbol, ")")
	p.expect(TokenSymbol, ";")

	return stmt, nil
}

// parseSelect: SELECT * FROM name [WHERE expr];
func (p *Parser) parseSelect() (*SelectStmt, error) {
	p.advance() // SELECT

	stmt := &SelectStmt{}

	// 列列表
	if p.peek().Value == "*" {
		p.advance()
		stmt.Columns = []string{"*"}
	} else {
		for {
			colTok, _ := p.expect(TokenIdentifier)
			stmt.Columns = append(stmt.Columns, colTok.Value)
			if p.peek().Value != "," {
				break
			}
			p.advance()
		}
	}

	p.expect(TokenKeyword, "FROM")
	tok, _ := p.expect(TokenIdentifier)
	stmt.TableName = tok.Value

	if strings.EqualFold(p.peek().Value, "WHERE") {
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		stmt.Where = expr
	}

	p.expect(TokenSymbol, ";")
	return stmt, nil
}

// === 表达式解析 ===

// parseExpr: 解析 AND/OR 逻辑
func (p *Parser) parseExpr() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}

	for strings.EqualFold(p.peek().Value, "AND") || strings.EqualFold(p.peek().Value, "OR") {
		op := p.advance().Value
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: strings.ToUpper(op), Left: left, Right: right}
	}

	return left, nil
}

// parseComparison: 解析比较运算 =, <>, <, >, <=, >=
func (p *Parser) parseComparison() (Expr, error) {
	left, err := p.parsePrimaryExpr()
	if err != nil {
		return nil, err
	}

	if isComparisonOp(p.peek().Value) {
		op := p.advance().Value
		right, err := p.parsePrimaryExpr()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}

	return left, nil
}

func isComparisonOp(s string) bool {
	switch s {
	case "=", "<>", "<", ">", "<=", ">=":
		return true
	}
	return false
}

// parsePrimaryExpr: 数字、字符串、标识符、NULL
func (p *Parser) parsePrimaryExpr() (Expr, error) {
	tok := p.peek()

	switch tok.Type {
	case TokenNumber:
		p.advance()
		v, _ := strconv.Atoi(tok.Value)
		return &Literal{Value: v}, nil
	case TokenString:
		p.advance()
		return &Literal{Value: tok.Value}, nil
	case TokenKeyword:
		if strings.EqualFold(tok.Value, "NULL") {
			p.advance()
			return &Literal{Value: nil}, nil
		}
		if strings.EqualFold(tok.Value, "TRUE") {
			p.advance()
			return &Literal{Value: true}, nil
		}
		if strings.EqualFold(tok.Value, "FALSE") {
			p.advance()
			return &Literal{Value: false}, nil
		}
		return nil, fmt.Errorf("unexpected keyword in expression: %s", tok.Value)
	case TokenIdentifier:
		p.advance()
		return &Identifier{Name: tok.Value}, nil
	default:
		return nil, fmt.Errorf("unexpected token in expression: %s", tok.Value)
	}
}

// ParseSQL 便捷函数
func ParseSQL(input string) (Statement, error) {
	parser, err := NewParser(input)
	if err != nil {
		return nil, err
	}
	return parser.Parse()
}
