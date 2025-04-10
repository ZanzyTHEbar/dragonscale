# Dragonscale

> [!IMPORTANT]\
> This project is in the early-stage of development. The API is unstable. All contributions are welcome.
> If you found this project useful, please consider sponsorship or support via Github Sponsors.
> If you really need this project for a specific application, you can hire me as a freelance developer. I am happy to work with teams implementing AI applications.

## 1. Introduction

### 1.1 Purpose

Dragonscale is a Golang library designed to provide an efficient execution runtime for LLM adapters. Inspired by LLMCompiler, it optimizes task execution through concurrency, hexagonal architecture, and advanced task scheduling, targeting applications requiring high performance and extensibility.

Dragonscale aims to facilitate the integration of community-defined tools and models, enabling developers to create scalable and adaptable systems for large language model applications.

### 1.2 Scope

This document outlines the design and implementation of Dragonscale, detailing its architecture, components, and strategies for task management, concurrency, and tool integrations.

### 1.3 References

- [LLMCompiler ICML 2024 Paper](https://arxiv.org/abs/2312.04511)

## 2. System Overview

### 2.1 Goals

- Provide a robust runtime for LLM adapters with efficient task scheduling.
- Support concurrent task execution with optimized resource management.
- Enable extensibility through community-defined tools using Model Context Protocol or WASM.
- Ensure modularity and adaptability via hexagonal architecture and strategy pattern.

### 2.2 Key Features

- **Task Scheduler**: Priority-based scheduling for one-time, fixed-rate, and fixed-delay tasks.
- **Concurrency**: Worker pool with automated sizing for parallel execution.
- **DAG Integration**: Dynamic and static Directed Acyclic Graphs (DAGs) with caching, pre-emption, and advanced optimization.
- **Extensibility**: Plug-in tools via configuration files or WASM modules with sandboxing.
- **Event Bus**: Loose coupling between components for scalability and flexibility.

### 2.3 LLMCompiler Diagram

Below is a Mermaid diagram illustrating the high-level architecture of LLMCompiler, which inspires Dragonscale:

```mermaid
graph TD
    A[Input Tasks] --> B[LLMCompiler Core]
    B --> C[Task Decomposition]
    B --> D[Dependency Analysis]
    C --> E[DAG Generation]
    D --> E
    E --> F[Task Scheduler]
    F --> G[Execution Engine]
    G --> H[Output Results]
    G --> I[Tool Integration]
```

### 2.4 Dragonscale Diagram

Below is a Mermaid diagram representing Dragonscale's architecture:

```mermaid
graph TD
    A[Task Input] --> B[Task Scheduler]
    B --> C[Min-Heap Queue]
    B --> D[Worker Pool]
    D --> E[Task Execution]
    E --> F[Event Bus]
    F --> G[Tool Manager]
    G --> H[Community Tools]
    E --> I[DAG Executor]
    I --> J[Static DAG Cache]
    I --> K[Dynamic DAG Delta]
    E --> L[Output Results]
```

## 3. Requirements

### 3.1 Functional Requirements

- **Task Management**: Create, schedule, and stop tasks dynamically with attributes like priority and execution type.
- **Task Execution**: Execute tasks based on priority and scheduled time, supporting multiple execution types.
- **Concurrency**: Manage multiple tasks concurrently with an adaptive worker pool.
- **Tool Integration**: Integrate community-defined tools securely.

### 3.2 Non-Functional Requirements

- **Scalability**: Handle large task volumes efficiently.
- **Performance**: Optimize resource usage with advanced algorithms.
- **Thread Safety**: Ensure safe concurrent operations.
- **Extensibility**: Support future enhancements seamlessly.

## 4. Architecture

### 4.1 Hexagonal Architecture

Dragonscale uses a hexagonal architecture:

- **Core Domain**: Task scheduling and execution logic.
- **Ports**: Interfaces for task management and tool integration.
- **Adapters**: Worker pools, event bus, and tool implementations.

### 4.2 Components

- **Task Scheduler**: Priority-based task management.
- **Worker Pool**: Concurrent execution with automated sizing.
- **Event Bus**: Decoupled component communication.
- **Strategy Pattern**: Flexible execution strategies.
- **Tool Manager**: Sandboxed tool integration.
- **DAG Executor**: Advanced DAG handling.
- **Event Topics**: Common event topics for integration.

## 5. Design Details

### 5.1 Task Scheduler

- Uses a min-heap for O(log n) scheduling

### 5.2 Worker Pool with Automated Sizing

- Automatically adjusts worker count based on system load:

### 5.3 DAG Integration with Advanced Optimization

Supports advanced DAG optimization algorithms:

- **Critical Path Method (CPM)**: Identifies the longest path to prioritize tasks.
- **List Scheduling with Priority**: Assigns tasks to workers based on priority and dependencies.
- **Dynamic DAG**: Supports dynamic updates with delta storage for efficient recomputation.
- **Caching**: Caches static DAGs for faster access.
- **Pre-emption**: Allows task pre-emption for better resource utilization.
- **Signal-Based Scheduling**: Uses signals to manage task execution and resource allocation.
- **Automated Worker Sizing**: Dynamically adjusts worker pool size based on system load.

## 6. Performance Optimization

- **Heap**: O(log n) task scheduling.
- **Signal-Based Scheduling**: Reduces CPU usage.
- **DAG Caching**: Minimizes recomputation.
- **Automated Worker Sizing**: Balances load dynamically.

## 7. Limitations and Solutions

### 7.1 Known Limitations

- **Dynamic DAG Complexity**: High overhead for frequent updates.
- **Concurrency Overhead**: Excessive goroutines may degrade performance.

### 7.2 Solutions

- **Dynamic DAG**: Use delta storage with periodic compaction to reduce complexity.
- **Concurrency**: Cap worker pool size with automated scaling limits.

### 7.3 Future Work

- **Advanced DAG Algorithms**: Integrate machine learning for predictive scheduling.
- **Tool Ecosystem**: Expand WASM support with a plugin marketplace.
