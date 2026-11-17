CREATE TABLE departments (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE
);

CREATE TABLE students (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(150) UNIQUE,
    department_id INTEGER REFERENCES departments(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_students_name
ON students(name);

INSERT INTO departments (name)
VALUES
    ('Informatics'),
    ('Information Systems');

INSERT INTO students (name, email, department_id)
VALUES
    ('Alice', 'alice@example.com', 1),
    ('Bob', 'bob@example.com', 2),
    ('Charlie', 'charlie@example.com', 1);