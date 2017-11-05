# Admission Control 机制

kubernetes 有3个层次的资源限制方式，分别在Container、Pod、Namespace 层次。
- Container层次主要利用容器本身的支持，比如Docker 对CPU、内存等的支持；
- Pod方面可以限制系统内创建Pod的资源范围，比如最大或者最小的CPU、memory需求；
- Namespace层次就是对用户级别的资源限额了，包括CPU、内存，还可以限定Pod、rc、service的数量。

