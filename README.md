# Coxwave Chatbot Server

이 프로젝트는 **네이버 스마트스토어**와 관련된 FAQ 질문에 대해 지능적인 응답을 제공하기 위한 **챗봇 서버**입니다. 벡터 검색(Vector Search), 정규식 기반 검색, GPT 기반의 자연어 처리 등을 결합하여 문맥에 맞는 응답을 생성합니다.

---

## 주요 기능

- **세션 관리**:  
  쿠키를 사용하여 세션 ID를 생성하고, 사용자별 대화 기록을 관리합니다.

- **대화 히스토리 제공**:  
  최근 3개의 사용자-챗봇 간 대화를 제공하여 문맥을 유지합니다.

- **실시간 WebSocket 통신**:  
  클라이언트와 서버 간 실시간 대화 지원.

- **RAG (Retrieval-Augmented Generation)**:

  - 벡터 검색과 GPT를 결합하여 정확하고 관련성 높은 응답 제공.
  - 벡터 검색 결과가 부족할 경우, 정규식을 사용하여 추가 데이터를 검색.

- **임베딩(Embedding) API**:  
  OpenAI의 임베딩 API를 사용하여 벡터 기반 유사성 검색 수행.

---

## 설치 및 실행 방법

### 1. 의존성 설치

이 프로젝트는 다음과 같은 외부 라이브러리를 사용합니다:

- [Gorilla Mux](https://github.com/gorilla/mux) - HTTP 라우터
- [Gorilla WebSocket](https://github.com/gorilla/websocket) - WebSocket 구현
- [PGX](https://github.com/jackc/pgx) - PostgreSQL 드라이버
- [GoDotEnv](https://github.com/joho/godotenv) - `.env` 파일에서 환경 변수를 로드

Go 프로젝트 의존성을 설치하려면 아래 명령어를 실행하세요:

```bash
go mod tidy
```

---

### 2. 환경 변수 설정

`.env` 파일을 생성하고 다음과 같은 환경 변수를 설정하세요:

```plantext
SERVER_PORT=8080
DB_HOST=localhost
DB_PORT=5432
DB_USER=your_db_user
DB_PASSWORD=your_db_password
OPENAI_API_KEY=your_openai_api_key
```

---

### 3. DB 설정

1. 데이터베이스 생성

```bash
psql -U postgres -d coxwave
```

2. 임베딩 데이터 임포트

```bash
psql -U postgres -d coxwave -f coxwave_backup.sql
```

---

### 4. 실행

서버 실행

```bash
go run main.go
```

---

클라이언트

http://localhost:8080
